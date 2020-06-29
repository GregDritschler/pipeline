/*
Copyright 2020 The Tekton Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tasklooprun

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tasklooprunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1beta1/tasklooprun"
	listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/names"
	"github.com/tektoncd/pipeline/pkg/reconciler/events"
	"github.com/tektoncd/pipeline/pkg/reconciler/tasklooprun/resources"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
)

const (
	// taskLoopLabelKey is used as the label identifier for a TaskLoop
	taskLoopLabelKey = "/taskLoop"

	// taskLoopRunLabelKey is used as the label identifier for a TaskLoopRun
	taskLoopRunLabelKey = "/taskLoopRun"

	// taskLoopIterationLabelKey is used as the label identifier for the iteration number
	taskLoopIterationLabelKey = "/taskLoopIteration"
)

// Reconciler implements controller.Reconciler for Configuration resources.
type Reconciler struct {
	PipelineClientSet clientset.Interface
	taskLoopLister    listers.TaskLoopLister
	taskLoopRunLister listers.TaskLoopRunLister
	taskRunLister     listers.TaskRunLister
}

var (
	// Check that our Reconciler implements tasklooprunreconciler.Interface
	_ tasklooprunreconciler.Interface = (*Reconciler)(nil)
)

// ReconcileKind compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the TaskLoopRun
// resource with the current status of the resource.
func (c *Reconciler) ReconcileKind(ctx context.Context, tlr *v1beta1.TaskLoopRun) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	logger.Infof("Reconciling TaskLoopRun %s/%s at %v", tlr.Namespace, tlr.Name, time.Now())
	recorder := controller.GetEventRecorder(ctx)

	// If the TaskLoopRun has not started, initialize the Condition and set the start time.
	if !tlr.IsStarted() {
		logger.Infof("Starting new TaskLoopRun %s/%s", tlr.Namespace, tlr.Name)
		tlr.Status.InitializeConditions()
		// In case node time was not synchronized, when controller has been scheduled to other nodes.
		if tlr.Status.StartTime.Sub(tlr.CreationTimestamp.Time) < 0 {
			logger.Warnf("TaskLoopRun %s createTimestamp %s is after the TaskLoopRun started %s", tlr.Name, tlr.CreationTimestamp, tlr.Status.StartTime)
			tlr.Status.StartTime = &tlr.CreationTimestamp
		}
		// Emit events. During the first reconcile the status of the TaskLoopRun may change twice
		// from not Started to Started and then to Running, so we need to sent the event here
		// and at the end of 'Reconcile' again.
		// We also want to send the "Started" event as soon as possible for anyone who may be waiting
		// on the event to perform user facing initialisations, such has reset a CI check status
		afterCondition := tlr.Status.GetCondition(apis.ConditionSucceeded)
		events.Emit(recorder, nil, afterCondition, tlr)
	}

	if tlr.IsDone() {
		logger.Infof("TaskLoopRun %s/%s is done", tlr.Namespace, tlr.Name)
		// TODO: metrics (metrics.DurationAndCount) -- this might not be right place (double counting)
		return nil
	}

	if tlr.IsCancelled() {
		logger.Infof("TaskLoopRun %s/%s is cancelled", tlr.Namespace, tlr.Name)
		// TODO: Implement cancel (cascade it to child taskruns)
		return nil
	}

	// Store the condition before reconcile
	beforeCondition := tlr.Status.GetCondition(apis.ConditionSucceeded)

	// Reconcile this copy of the TaskLoopRun
	err := c.reconcile(ctx, tlr)
	if err != nil {
		logger.Errorf("Reconcile error: %v", err.Error())
	}

	if err := c.updateLabelsAndAnnotations(tlr); err != nil {
		logger.Warn("Failed to update TaskLoopRun labels/annotations", zap.Error(err))
		// TODO: Include in multierror
	}

	afterCondition := tlr.Status.GetCondition(apis.ConditionSucceeded)
	events.Emit(recorder, beforeCondition, afterCondition, tlr)

	// TODO: Need to implement the multierror stuff?
	return err
}

func (c *Reconciler) reconcile(ctx context.Context, tlr *v1beta1.TaskLoopRun) error {
	logger := logging.FromContext(ctx)

	// Get the TaskLoop referenced by the TaskLoopRun
	taskLoopMeta, taskLoopSpec, err := c.getTaskLoop(tlr)
	if err != nil {
		return nil
	}

	// Store the fetched TaskLoopSpec on the TaskLoopRun for auditing
	storeTaskLoopSpec(tlr, taskLoopSpec)

	// Propagate labels and annotations from TaskLoop to TaskLoopRun.
	if tlr.Spec.TaskLoopRef != nil && tlr.Spec.TaskLoopRef.Name != "" {
		propagateTaskLoopLabelsAndAnnotations(tlr, taskLoopMeta)
	}

	// Validate TaskLoop spec
	if err := taskLoopSpec.Validate(ctx); err != nil {
		tlr.Status.MarkFailed(v1beta1.TaskLoopRunReasonFailedValidation.String(),
			"TaskLoop %s/%s can't be Run; it has an invalid spec: %s",
			taskLoopMeta.Namespace, taskLoopMeta.Name, err)
		return nil
	}

	// Update status of TaskRuns.  Return the TaskRun representing the highest loop iteration.
	highestIteration, highestIterationTr, err := c.updateTaskrunStatus(logger, tlr)
	if err != nil {
		return fmt.Errorf("error updating TaskRun status for TaskLoopRun %s/%s: %w", tlr.Namespace, tlr.Name, err)
	}

	// Check the status of the TaskRun for the highest iteration.
	if highestIterationTr != nil {
		// If it's not done, wait for it to finish.
		if !highestIterationTr.IsDone() {
			tlr.Status.MarkRunning(v1beta1.TaskLoopRunReasonRunning.String(),
				"Iterations completed: %d", highestIteration-1)
			return nil
		}
		// If it failed, then the TaskLoopRun has failed as well.
		if !highestIterationTr.IsSuccessful() {
			tlr.Status.MarkFailed(v1beta1.TaskLoopRunReasonFailed.String(),
				"TaskRun %s has failed",
				highestIterationTr.Name)
			return nil
		}
	}

	// The TaskRun for the highest iteration (if there was one) was successful.
	// Apply parameter substitution from the TaskLoopRun to the TaskLoop spec.
	// This resolves the withItems key which is necessary to determine the next item.
	taskLoopSpec = resources.ApplyParameters(taskLoopSpec, tlr)

	// Parse the task timeout.  Since this field supports parameter substitution
	// this must be done after the call to ApplyParameters.
	var timeout *metav1.Duration
	if taskLoopSpec.Task.Timeout != "" {
		duration, err := time.ParseDuration(taskLoopSpec.Task.Timeout)
		if err != nil {
			tlr.Status.MarkFailed(v1beta1.TaskLoopRunReasonFailedValidation.String(),
				"The timeout value in TaskLoop %s/%s is not valid: %s",
				taskLoopMeta.Namespace, taskLoopMeta.Name, err)
			return nil
		}
		if duration < 0 {
			tlr.Status.MarkFailed(v1beta1.TaskLoopRunReasonFailedValidation.String(),
				"The timeout value in TaskLoop %s/%s is negative",
				taskLoopMeta.Namespace, taskLoopMeta.Name)
			return nil
		}
		timeout = &metav1.Duration{Duration: duration}
	} else {
		// If there is no timeout then do not pass a timeout on the TaskRun.
		// The TaskRun will use the default timeout.
		timeout = nil
	}

	// Move on to the next iteration (or the first iteration if there was no TaskRun).
	// Check if the TaskLoopRun is done.
	nextIteration := highestIteration + 1
	if nextIteration > len(taskLoopSpec.WithItems) {
		tlr.Status.MarkSucceeded(v1beta1.TaskLoopRunReasonSucceeded.String(),
			"All TaskRuns completed successfully")
		return nil
	}

	// Substitute uses of $(item) in the TaskLoop with the current item.
	taskLoopSpec = resources.ApplyItem(taskLoopSpec, taskLoopSpec.WithItems[nextIteration-1])

	// Create a TaskRun to run this iteration.
	tr, err := c.createTaskRun(logger, taskLoopSpec, tlr, nextIteration, timeout)
	if err != nil {
		return fmt.Errorf("error creating TaskRun from TaskLoopRun %s: %w", tlr.Name, err)
	}

	tlr.Status.TaskRuns[tr.Name] = &v1beta1.TaskLoopTaskRunStatus{
		Iteration: nextIteration,
		Status:    &tr.Status,
	}

	tlr.Status.MarkRunning(v1beta1.TaskLoopRunReasonRunning.String(),
		"Iterations completed: %d", highestIteration)

	return nil
}

func (c *Reconciler) getTaskLoop(tlr *v1beta1.TaskLoopRun) (*metav1.ObjectMeta, *v1beta1.TaskLoopSpec, error) {
	taskLoopMeta := metav1.ObjectMeta{}
	taskLoopSpec := v1beta1.TaskLoopSpec{}
	if tlr.Spec.TaskLoopRef != nil && tlr.Spec.TaskLoopRef.Name != "" {
		tl, err := c.taskLoopLister.TaskLoops(tlr.Namespace).Get(tlr.Spec.TaskLoopRef.Name)
		if err != nil {
			tlr.Status.MarkFailed(v1beta1.TaskLoopRunReasonCouldntGetTaskLoop.String(),
				"Error retrieving TaskLoop for TaskLoopRun %s/%s: %s",
				tlr.Namespace, tlr.Name, err)
			return nil, nil, fmt.Errorf("Error retrieving TaskLoop for TaskLoopRun %s: %w", fmt.Sprintf("%s/%s", tlr.Namespace, tlr.Name), err)
		}
		taskLoopMeta = tl.ObjectMeta
		taskLoopSpec = tl.Spec
	} else if tlr.Spec.TaskLoopSpec != nil {
		taskLoopMeta = tlr.ObjectMeta
		taskLoopSpec = *tlr.Spec.TaskLoopSpec
	} else {
		// Missing taskLoopRef and taskLoopSpec should be caught by tasklooprun validation so this may be a hard path to hit.
		tlr.Status.MarkFailed(v1beta1.TaskLoopRunReasonCouldntGetTaskLoop.String(),
			"Missing taskLoopRef for TaskLoopRun %s/%s",
			tlr.Namespace, tlr.Name)
		return nil, nil, fmt.Errorf("Missing taskLoopRef for TaskLoopRun %s", fmt.Sprintf("%s/%s", tlr.Namespace, tlr.Name))
	}
	return &taskLoopMeta, &taskLoopSpec, nil
}

func (c *Reconciler) updateTaskrunStatus(logger *zap.SugaredLogger, tlr *v1beta1.TaskLoopRun) (int, *v1beta1.TaskRun, error) {
	highestIteration := 0
	var highestIterationTr *v1beta1.TaskRun = nil
	taskRunLabels := getTaskrunLabels(tlr, "")
	taskRuns, err := c.taskRunLister.TaskRuns(tlr.Namespace).List(labels.SelectorFromSet(taskRunLabels))
	if err != nil {
		logger.Errorf("could not list TaskRuns %#v", err)
		return 0, nil, err
	}
	if taskRuns == nil || len(taskRuns) == 0 {
		return 0, nil, nil
	}
	for _, tr := range taskRuns {
		lbls := tr.GetLabels()
		iterationStr := lbls[pipeline.GroupName+taskLoopIterationLabelKey]
		iteration, err := strconv.Atoi(iterationStr)
		if err != nil {
			logger.Errorf("Error converting iteration number in TaskRun %s:  %#v", tr.Name, err)
			// TODO: Fail the tasklooprun?
		}
		tlr.Status.TaskRuns[tr.Name] = &v1beta1.TaskLoopTaskRunStatus{
			Iteration: iteration,
			Status:    &tr.Status,
		}
		if iteration > highestIteration {
			highestIteration = iteration
			highestIterationTr = tr
		}
	}
	return highestIteration, highestIterationTr, nil
}

func (c *Reconciler) createTaskRun(logger *zap.SugaredLogger, tl *v1beta1.TaskLoopSpec, tlr *v1beta1.TaskLoopRun, iteration int, timeout *metav1.Duration) (*v1beta1.TaskRun, error) {

	// TODO: Investigate how task retries will be implemented.

	// Create name for TaskRun from TaskLoopRun name plus iteration number.
	trName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix(fmt.Sprintf("%s-%s", tlr.Name, fmt.Sprintf("%05d", iteration)))

	tr := &v1beta1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:            trName,
			Namespace:       tlr.Namespace,
			OwnerReferences: []metav1.OwnerReference{tlr.GetOwnerReference()},
			Labels:          getTaskrunLabels(tlr, strconv.Itoa(iteration)),
			Annotations:     getTaskrunAnnotations(tlr),
		},
		Spec: v1beta1.TaskRunSpec{
			Params:             tl.Task.Params,
			ServiceAccountName: tlr.Spec.ServiceAccountName,
			Timeout:            timeout,
			PodTemplate:        nil, // TODO: Implement pod template
		}}

	if tl.Task.TaskRef != nil {
		tr.Spec.TaskRef = &v1beta1.TaskRef{
			Name: tl.Task.TaskRef.Name,
			Kind: tl.Task.TaskRef.Kind,
		}
	} else if tl.Task.TaskSpec != nil {
		tr.Spec.TaskSpec = tl.Task.TaskSpec
	}

	logger.Infof("Creating a new TaskRun object %s", trName)
	return c.PipelineClientSet.TektonV1beta1().TaskRuns(tlr.Namespace).Create(tr)

}

func (c *Reconciler) updateLabelsAndAnnotations(tlr *v1beta1.TaskLoopRun) error {
	newTlr, err := c.taskLoopRunLister.TaskLoopRuns(tlr.Namespace).Get(tlr.Name)
	if err != nil {
		return fmt.Errorf("error getting TaskLoopRun %s when updating labels/annotations: %w", tlr.Name, err)
	}
	if !reflect.DeepEqual(tlr.ObjectMeta.Labels, newTlr.ObjectMeta.Labels) || !reflect.DeepEqual(tlr.ObjectMeta.Annotations, newTlr.ObjectMeta.Annotations) {
		mergePatch := map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels":      tlr.ObjectMeta.Labels,
				"annotations": tlr.ObjectMeta.Annotations,
			},
		}
		patch, err := json.Marshal(mergePatch)
		if err != nil {
			return err
		}
		_, err = c.PipelineClientSet.TektonV1beta1().TaskLoopRuns(tlr.Namespace).Patch(tlr.Name, types.MergePatchType, patch)
		return err
	}
	return nil
}

func getTaskrunAnnotations(tlr *v1beta1.TaskLoopRun) map[string]string {
	// Propagate annotations from TaskLoopRun to TaskRun.
	annotations := make(map[string]string, len(tlr.ObjectMeta.Annotations)+1)
	for key, val := range tlr.ObjectMeta.Annotations {
		annotations[key] = val
	}
	return annotations
}

func getTaskrunLabels(tlr *v1beta1.TaskLoopRun, iterationStr string) map[string]string {
	// Propagate labels from TaskLoopRun to TaskRun.
	labels := make(map[string]string, len(tlr.ObjectMeta.Labels)+1)
	for key, val := range tlr.ObjectMeta.Labels {
		labels[key] = val
	}
	labels[pipeline.GroupName+taskLoopRunLabelKey] = tlr.Name
	if iterationStr != "" {
		labels[pipeline.GroupName+taskLoopIterationLabelKey] = iterationStr
	}
	return labels
}

func propagateTaskLoopLabelsAndAnnotations(tlr *v1beta1.TaskLoopRun, taskLoopMeta *metav1.ObjectMeta) {
	// Propagate labels from TaskLoop to TaskLoopRun.
	if tlr.ObjectMeta.Labels == nil {
		tlr.ObjectMeta.Labels = make(map[string]string, len(taskLoopMeta.Labels)+1)
	}
	for key, value := range taskLoopMeta.Labels {
		tlr.ObjectMeta.Labels[key] = value
	}
	tlr.ObjectMeta.Labels[pipeline.GroupName+taskLoopLabelKey] = taskLoopMeta.Name

	// Propagate annotations from TaskLoop to TaskLoopRun.
	if tlr.ObjectMeta.Annotations == nil {
		tlr.ObjectMeta.Annotations = make(map[string]string, len(taskLoopMeta.Annotations))
	}
	for key, value := range taskLoopMeta.Annotations {
		tlr.ObjectMeta.Annotations[key] = value
	}
}

func storeTaskLoopSpec(tlr *v1beta1.TaskLoopRun, tls *v1beta1.TaskLoopSpec) {
	// Only store the TaskLoopSpec once, if it has never been set before.
	if tlr.Status.TaskLoopSpec == nil {
		tlr.Status.TaskLoopSpec = tls
	}
}
