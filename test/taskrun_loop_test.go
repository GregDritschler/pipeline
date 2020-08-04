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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1beta1 "knative.dev/pkg/apis/duck/v1beta1"
	knativetest "knative.dev/pkg/test"
)

var (
	startedEventMessage     = "" // TaskRun started event has no message
	ignoreReleaseAnnotation = func(k string, v string) bool {
		return k == pod.ReleaseAnnotation
	}
)

var aTask = &v1beta1.Task{
	ObjectMeta: metav1.ObjectMeta{
		Name: "a-task",
		Labels: map[string]string{
			"myTaskLabel": "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myTaskAnnotation": "myTaskAnnotationValue",
		},
	},
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

var runTaskLoopSuccess = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name: "run-taskloop",
		Labels: map[string]string{
			"myRunLabel": "myRunLabelValue",
		},
		Annotations: map[string]string{
			"myRunAnnotation": "myRunAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "dontfailonanyitem"},
		}},
		TaskRef:   &v1beta1.TaskRef{Name: "a-task"},
		WithItems: []string{"item1", "item2"},
	},
}

var runTaskLoopFailure = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name: "run-taskloop",
		Labels: map[string]string{
			"myRunLabel": "myRunLabelValue",
		},
		Annotations: map[string]string{
			"myRunAnnotation": "myRunAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}},
		TaskRef:   &v1beta1.TaskRef{Name: "a-task"},
		WithItems: []string{"item1", "item2"},
	},
}

var expectedTaskRunIteration1Success = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00001-", // does not include random suffix
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/parentTaskRun":     "run-taskloop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "1",
			"myRunLabel":                   "myRunLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myRunAnnotation":  "myRunAnnotationValue",
			"myTaskAnnotation": "myTaskAnnotationValue",
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
			"tekton.dev/parentTaskRun":     "run-taskloop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "2",
			"myRunLabel":                   "myRunLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myRunAnnotation":  "myRunAnnotationValue",
			"myTaskAnnotation": "myTaskAnnotationValue",
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
			"tekton.dev/parentTaskRun":     "run-taskloop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "1",
			"myRunLabel":                   "myRunLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myRunAnnotation":  "myRunAnnotationValue",
			"myTaskAnnotation": "myTaskAnnotationValue",
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
		task          *v1beta1.Task
		parentTaskRun *v1beta1.TaskRun
		// The following set of fields describe the expected outcome.
		expectedParentStatus  corev1.ConditionStatus
		expectedParentReason  v1beta1.TaskRunReason
		expectedChildTaskRuns []*v1beta1.TaskRun
		expectedEvents        []string
	}{{
		name:                  "looping TaskRun succeeds",
		task:                  aTask,
		parentTaskRun:         runTaskLoopSuccess,
		expectedParentStatus:  corev1.ConditionTrue,
		expectedParentReason:  v1beta1.TaskRunReasonSuccessful,
		expectedChildTaskRuns: []*v1beta1.TaskRun{expectedTaskRunIteration1Success, expectedTaskRunIteration2Success},
		expectedEvents:        []string{startedEventMessage, "Iterations completed: 0", "Iterations completed: 1", "All TaskRuns completed successfully"},
	}, {
		name:                  "looping TaskRun fails",
		task:                  aTask,
		parentTaskRun:         runTaskLoopFailure,
		expectedParentStatus:  corev1.ConditionFalse,
		expectedParentReason:  v1beta1.TaskRunReasonFailed,
		expectedChildTaskRuns: []*v1beta1.TaskRun{expectedTaskRunIteration1Failure},
		expectedEvents:        []string{startedEventMessage, "Iterations completed: 0", "TaskRun run-taskloop-00001-.* has failed"},
	}}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			tc := tc // Copy current tc to local variable due to test parallelization
			t.Parallel()
			c, namespace := setup(t)

			knativetest.CleanupOnInterrupt(func() { tearDown(t, c, namespace) }, t.Logf)
			defer tearDown(t, c, namespace)

			if tc.task != nil {
				if _, err := c.TaskClient.Create(tc.task); err != nil {
					t.Fatalf("Failed to create Task `%s`: %s", tc.task.Name, err)
				}
			}

			parentTaskRun, err := c.TaskRunClient.Create(tc.parentTaskRun)
			if err != nil {
				t.Fatalf("Failed to create TaskRun `%s`: %s", tc.parentTaskRun.Name, err)
			}

			t.Logf("Waiting for TaskRun %s/%s to complete", parentTaskRun.Namespace, parentTaskRun.Name)
			var inState ConditionAccessorFn
			var desc string
			if tc.expectedParentStatus == corev1.ConditionTrue {
				inState = Succeed(parentTaskRun.Name)
				desc = "TaskRunSucceed"
			} else {
				inState = FailedWithReason(tc.expectedParentReason.String(), parentTaskRun.Name)
				desc = "TaskRunFailed"
			}
			if err := WaitForTaskRunState(c, parentTaskRun.Name, inState, desc); err != nil {
				t.Fatalf("Error waiting for TaskRun %s/%s to finish: %s", parentTaskRun.Namespace, parentTaskRun.Name, err)
			}

			parentTaskRun, err = c.TaskRunClient.Get(parentTaskRun.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Couldn't get reconciled parent TaskRun %s/%s: %s", parentTaskRun.Namespace, parentTaskRun.Name, err)
			}

			t.Logf("Making sure the expected child TaskRuns were created")
			actualChildTaskRunList, err := c.TaskRunClient.List(metav1.ListOptions{LabelSelector: fmt.Sprintf("tekton.dev/parentTaskRun=%s", parentTaskRun.Name)})
			if err != nil {
				t.Fatalf("Error listing child TaskRuns for TaskRun %s/%s: %s", parentTaskRun.Namespace, parentTaskRun.Name, err)
			}

			if len(tc.expectedChildTaskRuns) != len(actualChildTaskRunList.Items) {
				t.Errorf("Expected %d child TaskRuns for parent TaskRun %s/%s but found %d",
					len(tc.expectedChildTaskRuns), parentTaskRun.Namespace, parentTaskRun.Name, len(actualChildTaskRunList.Items))
			}

			for i, expectedTaskRun := range tc.expectedChildTaskRuns {
				var actualTaskRun v1beta1.TaskRun
				found := false
				for _, actualTaskRun = range actualChildTaskRunList.Items {
					if strings.HasPrefix(actualTaskRun.Name, expectedTaskRun.Name) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected child TaskRun with prefix %s for parent TaskRun %s/%s not found",
						expectedTaskRun.Name, parentTaskRun.Namespace, parentTaskRun.Name)
					continue
				}
				if d := cmp.Diff(expectedTaskRun.Spec, actualTaskRun.Spec); d != "" {
					t.Errorf("Child TaskRun %s spec does not match expected spec. Diff %s", actualTaskRun.Name, diff.PrintWantGot(d))
				}
				if d := cmp.Diff(expectedTaskRun.ObjectMeta.Annotations, actualTaskRun.ObjectMeta.Annotations,
					cmpopts.IgnoreMapEntries(ignoreReleaseAnnotation)); d != "" {
					t.Errorf("Child TaskRun %s does not have expected annotations. Diff %s", actualTaskRun.Name, diff.PrintWantGot(d))
				}
				if d := cmp.Diff(expectedTaskRun.ObjectMeta.Labels, actualTaskRun.ObjectMeta.Labels); d != "" {
					t.Errorf("Child TaskRun %s does not have expected labels. Diff %s", actualTaskRun.Name, diff.PrintWantGot(d))
				}
				if d := cmp.Diff(expectedTaskRun.Status.Status.Conditions, actualTaskRun.Status.Status.Conditions,
					cmpopts.IgnoreTypes(apis.Condition{}.Message, apis.Condition{}.LastTransitionTime)); d != "" {
					t.Errorf("Child TaskRun %s does not have expected status condition. Diff %s", actualTaskRun.Name, diff.PrintWantGot(d))
				}

				// Check child TaskRun status in the parent TaskRun's status.
				childTaskRunStatusInParentTaskRun, exists := parentTaskRun.Status.IterationStatus[actualTaskRun.Name]
				if !exists {
					t.Errorf("Parent TaskRun status does not include status for child TaskRun %s", actualTaskRun.Name)
				} else {
					if d := cmp.Diff(expectedTaskRun.Status.Status.Conditions, childTaskRunStatusInParentTaskRun.Status.Status.Conditions,
						cmpopts.IgnoreTypes(apis.Condition{}.Message, apis.Condition{}.LastTransitionTime)); d != "" {
						t.Errorf("Status for child TaskRun %s in parent TaskRun %s does not have expected status condition. Diff %s",
							actualTaskRun.Name, parentTaskRun.Name, diff.PrintWantGot(d))
					}
					if i+1 != childTaskRunStatusInParentTaskRun.Iteration {
						t.Errorf("Status for child TaskRun %s in parent TaskRun %s has iteration number %d instead of %d",
							actualTaskRun.Name, parentTaskRun.Name, childTaskRunStatusInParentTaskRun.Iteration, i+1)
					}
				}
			}

			t.Logf("Checking events that were created from parent TaskRun")
			matchKinds := map[string][]string{"TaskRun": {parentTaskRun.Name}}
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
