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

package taskrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	jsonpatch "gomodules.xyz/jsonpatch/v2"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/names"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/logging"
)

const (
	// ParentTaskRunLabelKey is used as the label identifier in a child TaskRun for the parent TaskRun
	ParentTaskRunLabelKey = "/parentTaskRun"

	// taskLoopIterationLabelKey is used as the label identifier in a child TaskRun for the iteration number
	taskLoopIterationLabelKey = "/taskLoopIteration"
)

// reconcileLoop reconciles a parent TaskRun which iterates or loops a Task over a list of items.
func reconcileLoop(ctx context.Context, parentTr *v1beta1.TaskRun, pipelineClientSet clientset.Interface, taskRunLister listers.TaskRunLister) error {
	logger := logging.FromContext(ctx)
	logger.Infof("Reconciling parent taskrun %s/%s at %v", parentTr.Namespace, parentTr.Name, time.Now())

	// Update the status of the child TaskRuns.  Return the child TaskRun representing the highest loop iteration.
	highestIteration, highestIterationTr, err := updateChildTaskRunStatus(logger, taskRunLister, parentTr)
	if err != nil {
		return fmt.Errorf("error updating child TaskRun status for parent TaskRun %s/%s: %w", parentTr.Namespace, parentTr.Name, err)
	}

	// Check the status of the child TaskRun for the highest iteration.
	if highestIterationTr != nil {
		// If it's not done, wait for it to finish or cancel it if the run is cancelled.
		if !highestIterationTr.IsDone() {
			if parentTr.IsCancelled() {
				logger.Infof("Parent TaskRun %s/%s is cancelled.  Cancelling child TaskRun %s.", parentTr.Namespace, parentTr.Name, highestIterationTr.Name)
				b, err := getCancelPatch()
				if err != nil {
					return fmt.Errorf("Failed to make patch to cancel TaskRun %s: %v", highestIterationTr.Name, err)
				}
				if _, err := pipelineClientSet.TektonV1beta1().TaskRuns(parentTr.Namespace).Patch(highestIterationTr.Name, types.JSONPatchType, b, ""); err != nil {
					parentTr.Status.MarkResourceRunning(v1beta1.TaskRunReasonCouldntCancel.String(),
						"Failed to patch TaskRun %s with cancellation: %s: %v", highestIterationTr.Name, err)
					return nil
				}
				// Update parent TaskRun status. It is still running until the child TaskRun is actually cancelled.
				parentTr.Status.MarkResourceRunning(v1beta1.TaskRunReasonRunning.String(),
					"Cancelling TaskRun %s", highestIterationTr.Name)
				return nil
			}
			// It's running and not cancelled.
			parentTr.Status.MarkResourceRunning(v1beta1.TaskRunReasonRunning.String(),
				"Iterations completed: %d", highestIteration-1)
			return nil
		}
		// If it failed, then retry it if possible.  Otherwise fail the parent TaskRun.
		if !highestIterationTr.IsSuccessful() {
			if parentTr.IsCancelled() {
				parentTr.Status.MarkResourceFailed(v1beta1.TaskRunReasonCancelled,
					fmt.Errorf("TaskRun %s/%s was cancelled", parentTr.Namespace, parentTr.Name))
				parentTr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
			} else {
				retriesDone := len(highestIterationTr.Status.RetriesStatus)
				retries := 0 // TODO: TaskRun doesn't have a retries key currently
				if retriesDone < retries {
					highestIterationTr, err = retryTaskRun(pipelineClientSet, highestIterationTr)
					if err != nil {
						return fmt.Errorf("error retrying child TaskRun %s from parent TaskRun %s: %w", highestIterationTr.Name, parentTr.Name, err)
					}
					parentTr.Status.IterationStatus[highestIterationTr.Name] = &v1beta1.TaskIterationStatus{
						Iteration: highestIteration,
						Status:    &highestIterationTr.Status,
					}
				} else {
					parentTr.Status.MarkResourceFailed(v1beta1.TaskRunReasonFailed,
						fmt.Errorf("TaskRun %s has failed", highestIterationTr.Name))
					parentTr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
				}
			}
			return nil
		}
	}

	// Move on to the next iteration (or the first iteration if there was no TaskRun).
	// Check if the TaskRun is done.
	nextIteration := highestIteration + 1
	if nextIteration > len(parentTr.Spec.WithItems) {
		parentTr.Status.SetCondition(&apis.Condition{
			Type:    apis.ConditionSucceeded,
			Status:  corev1.ConditionTrue,
			Reason:  v1beta1.TaskRunReasonSuccessful.String(),
			Message: "All iterations completed successfully",
		})
		parentTr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
		return nil
	}

	// Before starting up another child TaskRun, check if the parent was cancelled.
	if parentTr.IsCancelled() {
		parentTr.Status.MarkResourceFailed(v1beta1.TaskRunReasonCancelled,
			fmt.Errorf("TaskRun %s/%s was cancelled", parentTr.Namespace, parentTr.Name))
		parentTr.Status.CompletionTime = &metav1.Time{Time: time.Now()}
		return nil
	}

	// Substitute uses of $(item) in the TaskRun's parameters with the current item.
	// This creates a copy of the TaskRun spec.
	parentTrSpec := applyItem(&parentTr.Spec, nextIteration-1)

	// Create a child TaskRun to run this iteration.
	childTr, err := createChildTaskRun(logger, pipelineClientSet, parentTr, parentTrSpec, nextIteration)
	if err != nil {
		return fmt.Errorf("error creating child TaskRun for parent TaskRun %s/%s: %w", parentTr.Namespace, parentTr.Name, err)
	}

	parentTr.Status.IterationStatus[childTr.Name] = &v1beta1.TaskIterationStatus{
		Iteration: nextIteration,
		Status:    &childTr.Status,
	}

	parentTr.Status.MarkResourceRunning(v1beta1.TaskRunReasonRunning.String(),
		"Iterations completed: %d", highestIteration)

	return nil
}

func updateChildTaskRunStatus(logger *zap.SugaredLogger, taskRunLister listers.TaskRunLister, parentTr *v1beta1.TaskRun) (int, *v1beta1.TaskRun, error) {
	if parentTr.Status.IterationStatus == nil {
		parentTr.Status.IterationStatus = make(map[string]*v1beta1.TaskIterationStatus)
	}
	highestIteration := 0
	var highestIterationTr *v1beta1.TaskRun = nil
	taskRunLabels := getTaskRunLabels(parentTr, "")
	taskRuns, err := taskRunLister.TaskRuns(parentTr.Namespace).List(labels.SelectorFromSet(taskRunLabels))
	if err != nil {
		return 0, nil, fmt.Errorf("could not list TaskRuns %#v", err)
	}
	if taskRuns == nil || len(taskRuns) == 0 {
		return 0, nil, nil
	}
	for _, childTr := range taskRuns {
		lbls := childTr.GetLabels()
		iterationStr := lbls[pipeline.GroupName+taskLoopIterationLabelKey]
		iteration, err := strconv.Atoi(iterationStr)
		if err != nil {
			// TODO: As it stands this error will cause reconcile to be retried. Should it be? Or should the run fail? Need hard vs soft error.
			return 0, nil, fmt.Errorf("Error converting iteration number in TaskRun %s:  %#v", childTr.Name, err)
		}
		parentTr.Status.IterationStatus[childTr.Name] = &v1beta1.TaskIterationStatus{
			Iteration: iteration,
			Status:    &childTr.Status,
		}
		if iteration > highestIteration {
			highestIteration = iteration
			highestIterationTr = childTr
		}
	}
	return highestIteration, highestIterationTr, nil
}

func createChildTaskRun(logger *zap.SugaredLogger, pipelineClientSet clientset.Interface, parentTr *v1beta1.TaskRun, parentTrSpec *v1beta1.TaskRunSpec, iteration int) (*v1beta1.TaskRun, error) {

	// Create name for TaskRun from TaskRun name plus iteration number.
	trName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix(fmt.Sprintf("%s-%s", parentTr.Name, fmt.Sprintf("%05d", iteration)))

	childTr := &v1beta1.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:            trName,
			Namespace:       parentTr.Namespace,
			OwnerReferences: []metav1.OwnerReference{parentTr.GetOwnerReference()},
			Labels:          getTaskRunLabels(parentTr, strconv.Itoa(iteration)),
			Annotations:     getTaskRunAnnotations(parentTr),
		},
		Spec: v1beta1.TaskRunSpec{
			Params:             parentTrSpec.Params,
			TaskRef:            parentTrSpec.TaskRef,
			TaskSpec:           parentTrSpec.TaskSpec,
			ServiceAccountName: parentTrSpec.ServiceAccountName,
			Timeout:            parentTrSpec.Timeout,
			PodTemplate:        parentTrSpec.PodTemplate,
		}}

	logger.Infof("Creating a new child TaskRun object %s/%s", childTr.Namespace, childTr.Name)
	return pipelineClientSet.TektonV1beta1().TaskRuns(childTr.Namespace).Create(childTr)

}

func retryTaskRun(pipelineClientSet clientset.Interface, tr *v1beta1.TaskRun) (*v1beta1.TaskRun, error) {
	newStatus := *tr.Status.DeepCopy()
	newStatus.RetriesStatus = nil
	tr.Status.RetriesStatus = append(tr.Status.RetriesStatus, newStatus)
	tr.Status.StartTime = nil
	tr.Status.CompletionTime = nil
	tr.Status.PodName = ""
	tr.Status.SetCondition(&apis.Condition{
		Type:   apis.ConditionSucceeded,
		Status: corev1.ConditionUnknown,
	})
	return pipelineClientSet.TektonV1beta1().TaskRuns(tr.Namespace).UpdateStatus(tr)
}

func getCancelPatch() ([]byte, error) {
	patches := []jsonpatch.JsonPatchOperation{{
		Operation: "add",
		Path:      "/spec/status",
		Value:     v1beta1.TaskRunSpecStatusCancelled,
	}}
	patchBytes, err := json.Marshal(patches)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal patch bytes in order to cancel: %v", err)
	}
	return patchBytes, nil
}

func applyItem(spec *v1beta1.TaskRunSpec, itemindex int) *v1beta1.TaskRunSpec {
	spec = spec.DeepCopy()
	stringReplacements := map[string]string{"item": spec.WithItems[itemindex]}
	// Perform substitutions in task parameters
	for i := range spec.Params {
		spec.Params[i].Value.ApplyReplacements(stringReplacements, nil)
	}
	return spec
}

func getTaskRunAnnotations(tr *v1beta1.TaskRun) map[string]string {
	annotations := make(map[string]string, len(tr.ObjectMeta.Annotations)+1)
	for key, val := range tr.ObjectMeta.Annotations {
		annotations[key] = val
	}
	return annotations
}

func getTaskRunLabels(tr *v1beta1.TaskRun, iterationStr string) map[string]string {
	labels := make(map[string]string, len(tr.ObjectMeta.Labels)+1)
	for key, val := range tr.ObjectMeta.Labels {
		labels[key] = val
	}
	labels[pipeline.GroupName+ParentTaskRunLabelKey] = tr.Name
	if iterationStr != "" {
		labels[pipeline.GroupName+taskLoopIterationLabelKey] = iterationStr
	}
	return labels
}
