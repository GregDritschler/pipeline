// +build e2e

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

package test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/pod"
	"github.com/tektoncd/pipeline/test/diff"
	"go.opencensus.io/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"knative.dev/pkg/apis"
	duckv1beta1 "knative.dev/pkg/apis/duck/v1beta1"
	knativetest "knative.dev/pkg/test"
)

var (
	startedEventMessage     = "" // TaskLoopRun started event has no message
	taskloopRunTimeout      = 10 * time.Minute
	ignoreReleaseAnnotation = func(k string, v string) bool {
		return k == pod.ReleaseAnnotation
	}
)

var aTask = &v1beta1.Task{
	ObjectMeta: metav1.ObjectMeta{Name: "a-task"},
	Spec: v1beta1.TaskSpec{
		Params: []v1beta1.ParamSpec{{
			Name: "current-item",
			Type: v1beta1.ParamTypeString,
		}, {
			Name: "fail-on-item",
			Type: v1beta1.ParamTypeString,
		}},
		Steps: []v1beta1.Step{{
			Container: corev1.Container{
				Name:    "passfail",
				Image:   "ubuntu",
				Command: []string{"/bin/bash"},
				Args:    []string{"-c", "[[ $(params.current-item) != $(params.fail-on-item) ]]"},
			},
		}},
	},
}

var aTaskLoop = &v1beta1.TaskLoop{
	ObjectMeta: metav1.ObjectMeta{Name: "a-taskloop"},
	Spec: v1beta1.TaskLoopSpec{
		Params: []v1beta1.ParamSpec{{
			Name: "withItems-parameter",
			Type: v1beta1.ParamTypeArray,
		}, {
			Name: "fail-on-item",
			Type: v1beta1.ParamTypeString,
		}},
		WithItems: []string{"$(params.withItems-parameter)"},
		Task: v1beta1.TaskLoopTask{
			TaskRef: &v1beta1.TaskRef{Name: "a-task"},
			Params: []v1beta1.Param{{
				Name:  "current-item",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
			}, {
				Name:  "fail-on-item",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.fail-on-item)"},
			}},
		},
	},
}

var runTaskLoopSuccess = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name: "run-taskloop",
		Labels: map[string]string{
			"myTestLabel": "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
		},
	},
	Spec: v1beta1.TaskLoopRunSpec{
		Params: []v1beta1.Param{{
			Name:  "withItems-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"item1", "item2"}},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "dontfailonanyitem"},
		}},
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "a-taskloop"},
	},
}

var runTaskLoopFailure = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name: "run-taskloop",
		Labels: map[string]string{
			"myTestLabel": "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
		},
	},
	Spec: v1beta1.TaskLoopRunSpec{
		Params: []v1beta1.Param{{
			Name:  "withItems-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"item1", "item2"}},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}},
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "a-taskloop"},
	},
}

var expectedTaskRunIteration1Success = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00001-", // does not include random suffix
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoop":          "a-taskloop",
			"tekton.dev/taskLoopRun":       "run-taskloop",
			"tekton.dev/taskLoopIteration": "1",
			"myTestLabel":                  "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef: &v1beta1.TaskRef{Name: "a-task", Kind: "Task"},
		Timeout: &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "dontfailonanyitem"},
		}},
	},
	Status: v1beta1.TaskRunStatus{
		Status: duckv1beta1.Status{
			Conditions: []apis.Condition{{
				Type:   apis.ConditionSucceeded,
				Status: corev1.ConditionTrue,
				Reason: v1beta1.TaskRunReasonSuccessful.String(),
			}},
		},
	},
}

var expectedTaskRunIteration2Success = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00002-", // does not include random suffix
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoop":          "a-taskloop",
			"tekton.dev/taskLoopRun":       "run-taskloop",
			"tekton.dev/taskLoopIteration": "2",
			"myTestLabel":                  "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef: &v1beta1.TaskRef{Name: "a-task", Kind: "Task"},
		Timeout: &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item2"},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "dontfailonanyitem"},
		}},
	},
	Status: v1beta1.TaskRunStatus{
		Status: duckv1beta1.Status{
			Conditions: []apis.Condition{{
				Type:   apis.ConditionSucceeded,
				Status: corev1.ConditionTrue,
				Reason: v1beta1.TaskRunReasonSuccessful.String(),
			}},
		},
	},
}

var expectedTaskRunIteration1Failure = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00001-", // does not include random suffix
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoop":          "a-taskloop",
			"tekton.dev/taskLoopRun":       "run-taskloop",
			"tekton.dev/taskLoopIteration": "1",
			"myTestLabel":                  "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef: &v1beta1.TaskRef{Name: "a-task", Kind: "Task"},
		Timeout: &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}},
	},
	Status: v1beta1.TaskRunStatus{
		Status: duckv1beta1.Status{
			Conditions: []apis.Condition{{
				Type:   apis.ConditionSucceeded,
				Status: corev1.ConditionFalse,
				Reason: v1beta1.TaskRunReasonFailed.String(),
			}},
		},
	},
}

func TestTaskLoopRun(t *testing.T) {
	t.Parallel()

	testcases := []struct {
		name string
		// The following set of fields describe the resources to create.
		task     *v1beta1.Task
		taskloop *v1beta1.TaskLoop
		run      *v1beta1.TaskLoopRun
		// The following set of fields describe the expected outcome.
		expectedStatus   corev1.ConditionStatus
		expectedReason   v1beta1.TaskLoopRunReason
		expectedTaskruns []*v1beta1.TaskRun
		expectedEvents   []string
	}{{
		name:             "successful TaskLoop",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              runTaskLoopSuccess,
		expectedStatus:   corev1.ConditionTrue,
		expectedReason:   v1beta1.TaskLoopRunReasonSucceeded,
		expectedTaskruns: []*v1beta1.TaskRun{expectedTaskRunIteration1Success, expectedTaskRunIteration2Success},
		expectedEvents:   []string{startedEventMessage, "Iterations completed: 0", "Iterations completed: 1", "All TaskRuns completed successfully"},
	}, {
		name:             "failed TaskLoop",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              runTaskLoopFailure,
		expectedStatus:   corev1.ConditionFalse,
		expectedReason:   v1beta1.TaskLoopRunReasonFailed,
		expectedTaskruns: []*v1beta1.TaskRun{expectedTaskRunIteration1Failure},
		expectedEvents:   []string{startedEventMessage, "Iterations completed: 0", "TaskRun run-taskloop-00001-.* has failed"},
	}}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, namespace := setup(t)

			knativetest.CleanupOnInterrupt(func() { tearDown(t, c, namespace) }, t.Logf)
			defer tearDown(t, c, namespace)

			if tc.task != nil {
				task := tc.task.DeepCopy()
				task.Namespace = namespace
				if _, err := c.TaskClient.Create(task); err != nil {
					t.Fatalf("Failed to create Task `%s`: %s", task.Name, err)
				}
			}

			if tc.taskloop != nil {
				taskloop := tc.taskloop.DeepCopy()
				taskloop.Namespace = namespace
				if _, err := c.TaskLoopClient.Create(taskloop); err != nil {
					t.Fatalf("Failed to create TaskLoop `%s`: %s", tc.taskloop.Name, err)
				}
			}

			// TODO: This is needed because taskloop reconciler is using lister to get taskloop.
			// Need to change reconciler to use k8 client.
			time.Sleep(2 * time.Second)

			run := tc.run.DeepCopy()
			run.Namespace = namespace
			run, err := c.TaskLoopRunClient.Create(tc.run)
			if err != nil {
				t.Fatalf("Failed to create TaskLoopRun `%s`: %s", run.Name, err)
			}

			t.Logf("Waiting for TaskLoopRun %s in namespace %s to complete", run.Name, run.Namespace)
			var inState ConditionAccessorFn
			var desc string
			if tc.expectedStatus == corev1.ConditionTrue {
				inState = Succeed(run.Name)
				desc = "TaskLoopRunSuccess"
			} else {
				inState = FailedWithReason(tc.expectedReason.String(), run.Name)
				desc = "TaskLoopRunFailed"
			}
			if err := waitForRunState(c, run.Name, taskloopRunTimeout, inState, desc); err != nil {
				t.Fatalf("Error waiting for TaskLoopRun %s/%s to finish: %s", run.Namespace, run.Name, err)
			}

			run, err = c.TaskLoopRunClient.Get(run.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Couldn't get expected TaskLoopRun %s/%s: %s", run.Namespace, run.Name, err)
			}

			t.Logf("Making sure the expected TaskRuns were created")
			actualTaskrunList, err := c.TaskRunClient.List(metav1.ListOptions{LabelSelector: fmt.Sprintf("tekton.dev/taskLoopRun=%s", run.Name)})
			if err != nil {
				t.Fatalf("Error listing TaskRuns for TaskLoopRun %s/%s: %s", run.Namespace, run.Name, err)
			}

			if len(tc.expectedTaskruns) != len(actualTaskrunList.Items) {
				t.Errorf("Expected %d TaskRuns for TaskLoopRun %s/%s but found %d",
					len(tc.expectedTaskruns), run.Namespace, run.Name, len(actualTaskrunList.Items))
			}

			for i, expectedTaskrun := range tc.expectedTaskruns {
				var actualTaskrun v1beta1.TaskRun
				found := false
				for _, actualTaskrun = range actualTaskrunList.Items {
					if strings.HasPrefix(actualTaskrun.Name, expectedTaskrun.Name) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected TaskRun with prefix %s for TaskLoopRun %s/%s not found",
						expectedTaskrun.Name, run.Namespace, run.Name)
					continue
				}
				if d := cmp.Diff(expectedTaskrun.Spec, actualTaskrun.Spec); d != "" {
					t.Errorf("TaskRun %s spec does not match expected spec. Diff %s", actualTaskrun.Name, diff.PrintWantGot(d))
				}
				if d := cmp.Diff(expectedTaskrun.ObjectMeta.Annotations, actualTaskrun.ObjectMeta.Annotations,
					cmpopts.IgnoreMapEntries(ignoreReleaseAnnotation)); d != "" {
					t.Errorf("TaskRun %s does not have expected annotations. Diff %s", actualTaskrun.Name, diff.PrintWantGot(d))
				}
				if d := cmp.Diff(expectedTaskrun.ObjectMeta.Labels, actualTaskrun.ObjectMeta.Labels); d != "" {
					t.Errorf("TaskRun %s does not have expected labels. Diff %s", actualTaskrun.Name, diff.PrintWantGot(d))
				}
				if d := cmp.Diff(expectedTaskrun.Status.Status.Conditions, actualTaskrun.Status.Status.Conditions,
					cmpopts.IgnoreTypes(apis.Condition{}.Message, apis.Condition{}.LastTransitionTime)); d != "" {
					t.Errorf("TaskRun %s does not have expected status condition. Diff %s", actualTaskrun.Name, diff.PrintWantGot(d))
				}

				// Check TaskRun status in the TaskLoopRun's status.
				taskRunStatusInTaskLoopRun, exists := run.Status.TaskRuns[actualTaskrun.Name]
				if !exists {
					t.Errorf("TaskLoopRun status does not include TaskRun status for TaskRun %s", actualTaskrun.Name)
				} else {
					if d := cmp.Diff(expectedTaskrun.Status.Status.Conditions, taskRunStatusInTaskLoopRun.Status.Status.Conditions,
						cmpopts.IgnoreTypes(apis.Condition{}.Message, apis.Condition{}.LastTransitionTime)); d != "" {
						t.Errorf("TaskLoopRun status for TaskRun %s does not have expected status condition. Diff %s",
							actualTaskrun.Name, diff.PrintWantGot(d))
					}
					if i+1 != taskRunStatusInTaskLoopRun.Iteration {
						t.Errorf("TaskLoopRun status for TaskRun %s has iteration number %d instead of %d",
							actualTaskrun.Name, taskRunStatusInTaskLoopRun.Iteration, i+1)
					}
				}
			}

			t.Logf("Checking events that were created from TaskLoopRun")
			matchKinds := map[string][]string{"TaskLoopRun": {run.Name}}
			events, err := collectMatchingEvents2(c.KubeClient, namespace, matchKinds)
			if err != nil {
				t.Fatalf("Failed to collect matching events: %q", err)
			}
			for e, expectedEvent := range tc.expectedEvents {
				if e >= len(events) {
					t.Errorf("Expected %d events but got %d", len(tc.expectedEvents), len(events))
					break
				}
				if matched, _ := regexp.MatchString(expectedEvent, events[e].Message); !matched {
					t.Errorf("Expected event %q but got %q", expectedEvent, events[e].Message)
				}
			}
		})
	}
}

// TODO: I assume this will be added to wait.go as part of custom tasks work.  Use that version instead.
func waitForRunState(c *clients, name string, polltimeout time.Duration, inState ConditionAccessorFn, desc string) error {
	metricName := fmt.Sprintf("WaitForRunState/%s/%s", name, desc)
	_, span := trace.StartSpan(context.Background(), metricName)
	defer span.End()

	interval := 1 * time.Second // defined in wait.go
	return wait.PollImmediate(interval, polltimeout, func() (bool, error) {
		r, err := c.TaskLoopRunClient.Get(name, metav1.GetOptions{})
		if err != nil {
			return true, err
		}
		return inState(&r.Status)
	})
}

// TODO: This is copied this from pipelinerun_test and modified to drop the reason parameter.
// Can a common version be made and shared somewhere? Maybe make reason optional somehow (pointer to string?).
//
// collectMatchingEvents collects list of events under 5 seconds that match
// 1. matchKinds which is a map of Kind of Object with name of objects
// 2. reason which is the expected reason of event  <<<<<< I DROPPED THIS
func collectMatchingEvents2(kubeClient *knativetest.KubeClient, namespace string, kinds map[string][]string) ([]*corev1.Event, error) {
	var events []*corev1.Event

	watchEvents, err := kubeClient.Kube.CoreV1().Events(namespace).Watch(metav1.ListOptions{})
	// close watchEvents channel
	defer watchEvents.Stop()
	if err != nil {
		return events, err
	}

	// create timer to not wait for events longer than 5 seconds
	timer := time.NewTimer(5 * time.Second)

	for {
		select {
		case wevent := <-watchEvents.ResultChan():
			event := wevent.Object.(*corev1.Event)
			if val, ok := kinds[event.InvolvedObject.Kind]; ok {
				for _, expectedName := range val {
					if event.InvolvedObject.Name == expectedName { // <<<< I DROPPED REASON CHECK
						events = append(events, event)
					}
				}
			}
		case <-timer.C:
			return events, nil
		}
	}
}
