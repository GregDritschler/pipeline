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
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/test"
	"github.com/tektoncd/pipeline/test/diff"
	"github.com/tektoncd/pipeline/test/names"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktesting "k8s.io/client-go/testing"
	"knative.dev/pkg/apis"
)

var (
	trueB = true
)

func requestCancel(parentTr *v1beta1.TaskRun) *v1beta1.TaskRun {
	trWithCancelStatus := parentTr.DeepCopy()
	trWithCancelStatus.Spec.Status = v1beta1.TaskRunSpecStatusCancelled
	return trWithCancelStatus
}

func running(tr *v1beta1.TaskRun) *v1beta1.TaskRun {
	trWithStatus := tr.DeepCopy()
	trWithStatus.Status.InitializeConditions()
	trWithStatus.Status.MarkResourceRunning(v1beta1.TaskRunReasonRunning.String(), "")
	return trWithStatus
}

func successful(tr *v1beta1.TaskRun) *v1beta1.TaskRun {
	trWithStatus := tr.DeepCopy()
	trWithStatus.Status.SetCondition(&apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionTrue,
		Reason:  v1beta1.TaskRunReasonSuccessful.String(),
		Message: "All Steps have completed executing",
	})
	return trWithStatus
}

func failed(tr *v1beta1.TaskRun) *v1beta1.TaskRun {
	trWithStatus := tr.DeepCopy()
	trWithStatus.Status.MarkResourceFailed(
		v1beta1.TaskRunReasonFailed,
		fmt.Errorf("Something went wrong"),
	)
	return trWithStatus
}

func getCreatedTaskrun(t *testing.T, clients test.Clients) *v1beta1.TaskRun {
	t.Log("actions", clients.Pipeline.Actions())
	for _, a := range clients.Pipeline.Actions() {
		if a.GetVerb() == "create" {
			obj := a.(ktesting.CreateAction).GetObject()
			if tr, ok := obj.(*v1beta1.TaskRun); ok {
				return tr
			}
		}
	}
	return nil
}

func checkTaskRunCondition(t *testing.T, tr *v1beta1.TaskRun, expectedStatus corev1.ConditionStatus, expectedReason v1beta1.TaskRunReason) {
	condition := tr.Status.GetCondition(apis.ConditionSucceeded)
	if condition == nil {
		t.Error("Condition missing in TaskRun")
	} else {
		if condition.Status != expectedStatus {
			t.Errorf("Expected TaskRun status to be %v but was %v", expectedStatus, condition.Status)
		}
		if condition.Reason != expectedReason.String() {
			t.Errorf("Expected TaskRun reason to be %q but was %q", expectedReason.String(), condition.Reason)
		}
	}
	if tr.Status.StartTime == nil {
		t.Errorf("Expected TaskRun start time to be set but it wasn't")
	}
	if expectedStatus == corev1.ConditionUnknown {
		if tr.Status.CompletionTime != nil {
			t.Errorf("Expected TaskRun completion time to not be set but it was")
		}
	} else if tr.Status.CompletionTime == nil {
		t.Errorf("Expected TaskRun completion time to be set but it wasn't")
	}
}

func checkTaskIterationStatus(t *testing.T, parentTr *v1beta1.TaskRun, expectedStatus map[string]v1beta1.TaskIterationStatus) {
	t.Log("taskruns", parentTr.Status.IterationStatus)
	if len(parentTr.Status.IterationStatus) != len(expectedStatus) {
		t.Errorf("Expected TaskRun iteration status to contain %d TaskRuns but it contains %d TaskRuns: %v",
			len(expectedStatus), len(parentTr.Status.IterationStatus), parentTr.Status.IterationStatus)
		return
	}
	for expectedTaskRunName, expectedChildTaskRunstatus := range expectedStatus {
		actualTaskRunStatus, exists := parentTr.Status.IterationStatus[expectedTaskRunName]
		if !exists {
			t.Errorf("Expected TaskRun iteration status to include TaskRun status for TaskRun %s", expectedTaskRunName)
			continue
		}
		if actualTaskRunStatus.Iteration != expectedChildTaskRunstatus.Iteration {
			t.Errorf("Iteration status for TaskRun %s has iteration number %d instead of %d",
				expectedTaskRunName, actualTaskRunStatus.Iteration, expectedChildTaskRunstatus.Iteration)
		}
		if d := cmp.Diff(expectedChildTaskRunstatus.Status, actualTaskRunStatus.Status, cmpopts.IgnoreTypes(apis.Condition{}.LastTransitionTime.Inner.Time)); d != "" {
			t.Errorf("Iteration status for TaskRun %s is incorrect. Diff %s", expectedTaskRunName, diff.PrintWantGot(d))
		}
	}
}

var aTask = &v1beta1.Task{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "a-task",
		Namespace: "foo",
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
			Name: "additional-parameter",
			Type: v1beta1.ParamTypeString,
		}},
		Steps: []v1beta1.Step{{
			Container: corev1.Container{Name: "foo", Image: "bar"},
		}},
	},
}

var aTaskRun = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop",
		Namespace: "foo",
		Labels: map[string]string{
			"myTaskRunLabel": "myTaskRunLabelValue",
		},
		Annotations: map[string]string{
			"myTaskRunAnnotation": "myTaskRunAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		TaskRef:   &v1beta1.TaskRef{Name: "a-task"},
		WithItems: []string{"item1", "item2"},
	},
}

var aTaskRunWithInlineTask = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-with-inline-task",
		Namespace: "foo",
		Labels: map[string]string{
			"myTaskRunLabel": "myTaskRunLabelValue",
		},
		Annotations: map[string]string{
			"myTaskRunAnnotation": "myTaskRunAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		TaskSpec: &v1beta1.TaskSpec{
			Params: []v1beta1.ParamSpec{{
				Name: "current-item",
				Type: v1beta1.ParamTypeString,
			}, {
				Name: "additional-parameter",
				Type: v1beta1.ParamTypeString,
			}},
			Steps: []v1beta1.Step{{
				Container: corev1.Container{Name: "foo", Image: "bar"},
			}},
		},
		WithItems: []string{"item1", "item2"},
	},
}

var expectedTaskRunIteration1 = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00001-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskRun",
			Name:               "run-taskloop",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/parentTaskRun":     "run-taskloop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "1",
			"myTaskLabel":                  "myTaskLabelValue",
			"myTaskRunLabel":               "myTaskRunLabelValue",
		},
		Annotations: map[string]string{
			"myTaskAnnotation":    "myTaskAnnotationValue",
			"myTaskRunAnnotation": "myTaskRunAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef: &v1beta1.TaskRef{Name: "a-task", Kind: "Task"},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		Timeout: &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
	},
}

// Note: The taskrun for the second iteration has the same random suffix as the first due to the resetting of the seed on each test.
var expectedTaskRunIteration2 = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00002-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskRun",
			Name:               "run-taskloop",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/parentTaskRun":     "run-taskloop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "2",
			"myTaskLabel":                  "myTaskLabelValue",
			"myTaskRunLabel":               "myTaskRunLabelValue",
		},
		Annotations: map[string]string{
			"myTaskAnnotation":    "myTaskAnnotationValue",
			"myTaskRunAnnotation": "myTaskRunAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef: &v1beta1.TaskRef{Name: "a-task", Kind: "Task"},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item2"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		Timeout: &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
	},
}

var expectedTaskRunWithInlineTaskIteration1 = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-with-inline-task-00001-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskRun",
			Name:               "run-taskloop-with-inline-task",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/parentTaskRun":     "run-taskloop-with-inline-task",
			"tekton.dev/taskLoopIteration": "1",
			"myTaskRunLabel":               "myTaskRunLabelValue",
		},
		Annotations: map[string]string{
			"myTaskRunAnnotation": "myTaskRunAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskSpec: &v1beta1.TaskSpec{
			Params: []v1beta1.ParamSpec{{
				Name: "current-item",
				Type: v1beta1.ParamTypeString,
			}, {
				Name: "additional-parameter",
				Type: v1beta1.ParamTypeString,
			}},
			Steps: []v1beta1.Step{{
				Container: corev1.Container{Name: "foo", Image: "bar"},
			}},
		},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		Timeout: &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
	},
}

func TestReconcileLoopingTaskRun(t *testing.T) {

	testcases := []struct {
		name string
		// The following set of fields describe the resources on entry to reconcile.
		task          *v1beta1.Task
		parentTaskRun *v1beta1.TaskRun
		childTaskRuns []*v1beta1.TaskRun
		// The following set of fields describe the expected state after reconcile.
		expectedParentStatus  corev1.ConditionStatus
		expectedParentReason  v1beta1.TaskRunReason
		expectedChildTaskRuns []*v1beta1.TaskRun
		expectedEvents        []string
	}{{
		name:                  "Reconcile new looping TaskRun with a referenced task",
		task:                  aTask,
		parentTaskRun:         aTaskRun,
		childTaskRuns:         []*v1beta1.TaskRun{},
		expectedParentStatus:  corev1.ConditionUnknown,
		expectedParentReason:  v1beta1.TaskRunReasonRunning,
		expectedChildTaskRuns: []*v1beta1.TaskRun{expectedTaskRunIteration1},
		expectedEvents:        []string{"Normal Started", "Normal Running Iterations completed: 0"},
	}, {
		name:                  "Reconcile new looping TaskRun with an inline task",
		parentTaskRun:         aTaskRunWithInlineTask,
		childTaskRuns:         []*v1beta1.TaskRun{},
		expectedParentStatus:  corev1.ConditionUnknown,
		expectedParentReason:  v1beta1.TaskRunReasonRunning,
		expectedChildTaskRuns: []*v1beta1.TaskRun{expectedTaskRunWithInlineTaskIteration1},
		expectedEvents:        []string{"Normal Started", "Normal Running Iterations completed: 0"},
	}, {
		name:                  "Reconcile looping TaskRun after the first iteration succeeded.",
		task:                  aTask,
		parentTaskRun:         running(aTaskRun),
		childTaskRuns:         []*v1beta1.TaskRun{successful(expectedTaskRunIteration1)},
		expectedParentStatus:  corev1.ConditionUnknown,
		expectedParentReason:  v1beta1.TaskRunReasonRunning,
		expectedChildTaskRuns: []*v1beta1.TaskRun{successful(expectedTaskRunIteration1), expectedTaskRunIteration2},
		expectedEvents:        []string{"Normal Running Iterations completed: 1"},
	}, {
		name:                  "Reconcile looping TaskRun after all iterations succeeded",
		task:                  aTask,
		parentTaskRun:         running(aTaskRun),
		childTaskRuns:         []*v1beta1.TaskRun{successful(expectedTaskRunIteration1), successful(expectedTaskRunIteration2)},
		expectedParentStatus:  corev1.ConditionTrue,
		expectedParentReason:  v1beta1.TaskRunReasonSuccessful,
		expectedChildTaskRuns: []*v1beta1.TaskRun{successful(expectedTaskRunIteration1), successful(expectedTaskRunIteration2)},
		expectedEvents:        []string{"Normal Succeeded All iterations completed successfully"},
	}, {
		name:                  "Reconcile looping TaskRun after the first iteration failed",
		task:                  aTask,
		parentTaskRun:         running(aTaskRun),
		childTaskRuns:         []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedParentStatus:  corev1.ConditionFalse,
		expectedParentReason:  v1beta1.TaskRunReasonFailed,
		expectedChildTaskRuns: []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedEvents:        []string{"Warning Failed TaskRun " + expectedTaskRunIteration1.Name + " has failed"},
	}, {
		name:                  "Reconcile cancelled looping TaskRun while the first iteration is running",
		task:                  aTask,
		parentTaskRun:         requestCancel(running(aTaskRun)),
		childTaskRuns:         []*v1beta1.TaskRun{running(expectedTaskRunIteration1)},
		expectedParentStatus:  corev1.ConditionUnknown,
		expectedParentReason:  v1beta1.TaskRunReasonRunning,
		expectedChildTaskRuns: []*v1beta1.TaskRun{running(expectedTaskRunIteration1)},
		expectedEvents:        []string{"Normal Running Cancelling TaskRun " + expectedTaskRunIteration1.Name},
	}, {
		name:                  "Reconcile cancelled looping TaskRun after the first iteration failed",
		task:                  aTask,
		parentTaskRun:         requestCancel(running(aTaskRun)),
		childTaskRuns:         []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedParentStatus:  corev1.ConditionFalse,
		expectedParentReason:  v1beta1.TaskRunReasonCancelled,
		expectedChildTaskRuns: []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedEvents:        []string{"Warning Failed TaskRun " + aTaskRun.Namespace + "/" + aTaskRun.Name + " was cancelled"},
	}, {
		name:                  "Reconcile cancelled taskloop run after the first iteration succeeded",
		task:                  aTask,
		parentTaskRun:         requestCancel(running(aTaskRun)),
		childTaskRuns:         []*v1beta1.TaskRun{successful(expectedTaskRunIteration1)},
		expectedParentStatus:  corev1.ConditionFalse,
		expectedParentReason:  v1beta1.TaskRunReasonCancelled,
		expectedChildTaskRuns: []*v1beta1.TaskRun{successful(expectedTaskRunIteration1)},
		expectedEvents:        []string{"Warning Failed TaskRun " + aTaskRun.Namespace + "/" + aTaskRun.Name + " was cancelled"},
	}}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			names.TestingSeed()

			optionalTask := []*v1beta1.Task{tc.task}
			if tc.task == nil {
				optionalTask = nil
			}

			d := test.Data{
				Tasks:    optionalTask,
				TaskRuns: append(tc.childTaskRuns, tc.parentTaskRun),
			}

			testAssets, cancel := getTaskRunController(t, d)
			defer cancel()
			c := testAssets.Controller
			clients := testAssets.Clients

			// Reconcile the parent TaskRun.
			if err := c.Reconciler.Reconcile(context.Background(), getRunName(tc.parentTaskRun)); err != nil {
				t.Fatalf("Error reconciling: %s", err)
			}

			// Fetch the updated parent TaskRun.
			reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskRuns(tc.parentTaskRun.Namespace).Get(tc.parentTaskRun.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Error getting reconciled run from fake client: %s", err)
			}

			// Verify that the parent TaskRun has the expected status and reason.
			checkTaskRunCondition(t, reconciledRun, tc.expectedParentStatus, tc.expectedParentReason)

			// Verify that a child TaskRun was or was not created depending on the test.
			// If the number of expected child TaskRuns is greater than the original number of child TaskRuns
			// then the test expects a new TaskRun to be created.  The new TaskRun must be the
			// last one in the list of expected child TaskRuns.
			createdTaskrun := getCreatedTaskrun(t, clients)
			if len(tc.expectedChildTaskRuns) > len(tc.childTaskRuns) {
				if createdTaskrun == nil {
					t.Errorf("A child TaskRun should have been created but was not")
				} else {
					if d := cmp.Diff(tc.expectedChildTaskRuns[len(tc.expectedChildTaskRuns)-1], createdTaskrun); d != "" {
						t.Errorf("Expected child TaskRun was not created. Diff %s", diff.PrintWantGot(d))
					}
				}
			} else {
				if createdTaskrun != nil {
					t.Errorf("A child TaskRun was created which was not expected")
				}
			}

			// Verify TaskRun status contains status for all TaskRuns.
			expectedChildTaskRuns := map[string]v1beta1.TaskIterationStatus{}
			for i, tr := range tc.expectedChildTaskRuns {
				expectedChildTaskRuns[tr.Name] = v1beta1.TaskIterationStatus{Iteration: i + 1, Status: &tr.Status}
			}
			checkTaskIterationStatus(t, reconciledRun, expectedChildTaskRuns)

			// Verify expected events were created.
			if err := checkEvents(t, testAssets.Recorder, tc.name, tc.expectedEvents); err != nil {
				t.Errorf(err.Error())
			}
		})
	}
}
