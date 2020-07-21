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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	ttesting "github.com/tektoncd/pipeline/pkg/reconciler/testing"
	"github.com/tektoncd/pipeline/pkg/system"
	"github.com/tektoncd/pipeline/test"
	"github.com/tektoncd/pipeline/test/diff"
	"github.com/tektoncd/pipeline/test/names"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
)

var (
	namespace = ""
	images    = pipeline.Images{
		EntrypointImage:          "override-with-entrypoint:latest",
		NopImage:                 "tianon/true",
		AffinityAssistantImage:   "nginx",
		GitImage:                 "override-with-git:latest",
		CredsImage:               "override-with-creds:latest",
		KubeconfigWriterImage:    "override-with-kubeconfig-writer:latest",
		ShellImage:               "busybox",
		GsutilImage:              "google/cloud-sdk",
		BuildGCSFetcherImage:     "gcr.io/cloud-builders/gcs-fetcher:latest",
		PRImage:                  "override-with-pr:latest",
		ImageDigestExporterImage: "override-with-imagedigest-exporter-image:latest",
	}
	trueB = true
)

func getRunName(tlr *v1beta1.TaskLoopRun) string {
	return strings.Join([]string{tlr.Namespace, tlr.Name}, "/")
}

func loopRunning(tlr *v1beta1.TaskLoopRun) *v1beta1.TaskLoopRun {
	runWithStatus := tlr.DeepCopy()
	runWithStatus.Status.InitializeConditions()
	runWithStatus.Status.MarkRunning(v1beta1.TaskLoopRunReasonRunning.String(), "")
	return runWithStatus
}

func requestCancel(tlr *v1beta1.TaskLoopRun) *v1beta1.TaskLoopRun {
	runWithCancelStatus := tlr.DeepCopy()
	runWithCancelStatus.Spec.Status = v1beta1.TaskLoopRunSpecStatusCancelled
	return runWithCancelStatus
}

func allowRetry(tl *v1beta1.TaskLoop) *v1beta1.TaskLoop {
	taskLoopWithRetries := tl.DeepCopy()
	taskLoopWithRetries.Spec.Task.Retries = 1
	return taskLoopWithRetries
}

func running(tr *v1beta1.TaskRun) *v1beta1.TaskRun {
	trWithStatus := tr.DeepCopy()
	trWithStatus.Status.SetCondition(&apis.Condition{
		Type:   apis.ConditionSucceeded,
		Status: corev1.ConditionUnknown,
		Reason: v1beta1.TaskRunReasonRunning.String(),
	})
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
	trWithStatus.Status.SetCondition(&apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionFalse,
		Reason:  v1beta1.TaskRunReasonFailed.String(),
		Message: "Something went wrong",
	})
	return trWithStatus
}

func retrying(tr *v1beta1.TaskRun) *v1beta1.TaskRun {
	trWithRetryStatus := tr.DeepCopy()
	trWithRetryStatus.Status.RetriesStatus = nil
	trWithRetryStatus.Status.RetriesStatus = append(tr.Status.RetriesStatus, trWithRetryStatus.Status)
	trWithRetryStatus.Status.SetCondition(&apis.Condition{
		Type:   apis.ConditionSucceeded,
		Status: corev1.ConditionUnknown,
	})
	return trWithRetryStatus
}

// getTaskLoopRunController returns an instance of the TaskLoopRun controller/reconciler that has been seeded with
// d, where d represents the state of the system (existing resources) needed for the test.
func getTaskLoopRunController(t *testing.T, d test.Data) (test.Assets, func()) {
	ctx, _ := ttesting.SetupFakeContext(t)
	ctx, cancel := context.WithCancel(ctx)
	c, informers := test.SeedTestData(t, ctx, d)
	configMapWatcher := configmap.NewInformedWatcher(c.Kube, system.GetNamespace())
	return test.Assets{
		Logger:     logging.FromContext(ctx),
		Controller: NewController(namespace, images)(ctx, configMapWatcher),
		Clients:    c,
		Informers:  informers,
		Recorder:   controller.GetEventRecorder(ctx).(*record.FakeRecorder),
	}, cancel
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

func checkEvents(fr *record.FakeRecorder, testName string, wantEvents []string) error {
	// The fake recorder runs in a go routine, so the timeout is here to avoid waiting
	// on the channel forever if fewer than expected events are received.
	// We only hit the timeout in case of failure of the test, so the actual value
	// of the timeout is not so relevant. It's only used when tests are going to fail.
	timer := time.NewTimer(1 * time.Second)
	foundEvents := []string{}
	for ii := 0; ii < len(wantEvents)+1; ii++ {
		// We loop over all the events that we expect. Once they are all received
		// we exit the loop. If we never receive enough events, the timeout takes us
		// out of the loop.
		select {
		case event := <-fr.Events:
			foundEvents = append(foundEvents, event)
			if ii > len(wantEvents)-1 {
				return fmt.Errorf(`Received extra event "%s" for test "%s"`, event, testName)
			}
			wantEvent := wantEvents[ii]
			if !(strings.HasPrefix(event, wantEvent)) {
				return fmt.Errorf(`Expected event "%s" but got "%s" instead for test "%s"`, wantEvent, event, testName)
			}
		case <-timer.C:
			if len(foundEvents) > len(wantEvents) {
				return fmt.Errorf(`Received %d events but %d expected for test "%s". Found events: %#v`, len(foundEvents), len(wantEvents), testName, foundEvents)
			}
		}
	}
	return nil
}

func checkTaskLoopRunCondition(t *testing.T, tlr *v1beta1.TaskLoopRun, expectedStatus corev1.ConditionStatus, expectedReason v1beta1.TaskLoopRunReason) {
	condition := tlr.Status.GetCondition(apis.ConditionSucceeded)
	if condition == nil {
		t.Error("Condition missing in TaskLoopRun")
	} else {
		if condition.Status != expectedStatus {
			t.Errorf("Expected TaskLoopRun status to be %v but was %v", expectedStatus, condition)
		}
		if condition.Reason != expectedReason.String() {
			t.Errorf("Expected reason to be %q but was %q", expectedReason.String(), condition.Reason)
		}
	}
	if tlr.Status.StartTime == nil {
		t.Errorf("Expected TaskLoopRun start time to be set but it wasn't")
	}
	if expectedStatus == corev1.ConditionUnknown {
		if tlr.Status.CompletionTime != nil {
			t.Errorf("Expected TaskLoopRun completion time to not be set but it was")
		}
	} else if tlr.Status.CompletionTime == nil {
		t.Errorf("Expected TaskLoopRun completion time to be set but it wasn't")
	}
}

func checkTaskLoopRunStatus(t *testing.T, tlr *v1beta1.TaskLoopRun, expectedStatus map[string]v1beta1.TaskLoopTaskRunStatus) {
	// TODO: I will need to use Run function getAdditionalFields("taskruns")
	t.Log("taskruns", tlr.Status.TaskRuns)
	if len(tlr.Status.TaskRuns) != len(expectedStatus) {
		t.Errorf("Expected TaskLoopRun status to include two TaskRuns: %v", tlr.Status.TaskRuns)
		return
	}
	for expectedTaskRunName, expectedTaskRunStatus := range expectedStatus {
		actualTaskRunStatus, exists := tlr.Status.TaskRuns[expectedTaskRunName]
		if !exists {
			t.Errorf("Expected TaskLoopRun status to include TaskRun status for TaskRun %s", expectedTaskRunName)
			continue
		}
		if actualTaskRunStatus.Iteration != expectedTaskRunStatus.Iteration {
			t.Errorf("TaskLoopRun status for TaskRun %s has iteration number %d instead of %d",
				expectedTaskRunName, actualTaskRunStatus.Iteration, expectedTaskRunStatus.Iteration)
		}
		if d := cmp.Diff(expectedTaskRunStatus.Status, actualTaskRunStatus.Status, cmpopts.IgnoreTypes(apis.Condition{}.LastTransitionTime.Inner.Time)); d != "" {
			t.Errorf("TaskLoopRun status for TaskRun %s is incorrect. Diff %s", expectedTaskRunName, diff.PrintWantGot(d))
		}
	}
}

var aTask = &v1beta1.Task{
	ObjectMeta: metav1.ObjectMeta{Name: "a-task", Namespace: "foo"},
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

var aTaskLoop = &v1beta1.TaskLoop{
	ObjectMeta: metav1.ObjectMeta{Name: "a-taskloop", Namespace: "foo"},
	Spec: v1beta1.TaskLoopSpec{
		Params: []v1beta1.ParamSpec{{
			Name: "withItems-parameter",
			Type: v1beta1.ParamTypeArray,
		}, {
			Name:    "timeout-parameter",
			Type:    v1beta1.ParamTypeString,
			Default: &v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: ""},
		}, {
			Name: "additional-parameter",
			Type: v1beta1.ParamTypeString,
		}},
		WithItems: []string{"$(params.withItems-parameter)"},
		Task: v1beta1.TaskLoopTask{
			TaskRef: &v1beta1.TaskRef{Name: "a-task"},
			Timeout: "$(params.timeout-parameter)",
			Params: []v1beta1.Param{{
				Name:  "current-item",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
			}, {
				Name:  "additional-parameter",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.additional-parameter)"},
			}},
		},
	},
}

var aTaskLoopWithInlineTask = &v1beta1.TaskLoop{
	ObjectMeta: metav1.ObjectMeta{Name: "a-taskloop-with-inline-task", Namespace: "foo"},
	Spec: v1beta1.TaskLoopSpec{
		Params: []v1beta1.ParamSpec{{
			Name: "withItems-parameter",
			Type: v1beta1.ParamTypeArray,
		}, {
			Name:    "timeout-parameter",
			Type:    v1beta1.ParamTypeString,
			Default: &v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: ""},
		}, {
			Name: "additional-parameter",
			Type: v1beta1.ParamTypeString,
		}},
		WithItems: []string{"$(params.withItems-parameter)"},
		Task: v1beta1.TaskLoopTask{
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
			Timeout: "$(params.timeout-parameter)",
			Params: []v1beta1.Param{{
				Name:  "current-item",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
			}, {
				Name:  "additional-parameter",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.additional-parameter)"},
			}},
		},
	},
}

var runTaskLoop = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop",
		Namespace: "foo",
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
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "a-taskloop"},
	},
}

var runTaskLoopWithInlineTask = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-with-inline-task",
		Namespace: "foo",
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
			Name:  "timeout-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "5m"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "a-taskloop-with-inline-task"},
	},
}

var runWithNonexistentTaskLoop = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "bad-tasklooprun-taskloop-not-found",
		Namespace: "foo",
	},
	Spec: v1beta1.TaskLoopRunSpec{
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "no-such-taskloop"},
	},
}

var runTaskLoopWithInvalidTimeout = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "bad-tasklooprun-invalid-timeout",
		Namespace: "foo",
	},
	Spec: v1beta1.TaskLoopRunSpec{
		Params: []v1beta1.Param{{
			Name:  "withItems-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"item1", "item2"}},
		}, {
			Name:  "timeout-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "junk"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "a-taskloop"},
	},
}

var runTaskLoopWithNegativeTimeout = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "bad-tasklooprun-negative-timeout",
		Namespace: "foo",
	},
	Spec: v1beta1.TaskLoopRunSpec{
		Params: []v1beta1.Param{{
			Name:  "withItems-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"item1", "item2"}},
		}, {
			Name:  "timeout-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "-30s"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "a-taskloop"},
	},
}

var expectedTaskRunIteration1 = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00001-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskLoopRun",
			Name:               "run-taskloop",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
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
		TaskRef: &v1beta1.TaskRef{Name: "a-task"},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
	},
}

// Note: The taskrun for the second iteration has the same random suffix as the first due to the resetting of the seed on each test.
var expectedTaskRunIteration2 = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-00002-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskLoopRun",
			Name:               "run-taskloop",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
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
		TaskRef: &v1beta1.TaskRef{Name: "a-task"},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item2"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
	},
}

var expectedTaskRunWithInlineTaskIteration1 = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-taskloop-with-inline-task-00001-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskLoopRun",
			Name:               "run-taskloop-with-inline-task",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
			"tekton.dev/taskLoop":          "a-taskloop-with-inline-task",
			"tekton.dev/taskLoopRun":       "run-taskloop-with-inline-task",
			"tekton.dev/taskLoopIteration": "1",
			"myTestLabel":                  "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
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
		Timeout: &metav1.Duration{Duration: 5 * time.Minute},
	},
}

func TestReconcileTaskLoopRun(t *testing.T) {

	testcases := []struct {
		name string
		// The following set of fields describe the resources on entry to reconcile.
		task     *v1beta1.Task
		taskloop *v1beta1.TaskLoop
		run      *v1beta1.TaskLoopRun
		taskruns []*v1beta1.TaskRun
		// The following set of fields describe the expected state after reconcile.
		expectedStatus   corev1.ConditionStatus
		expectedReason   v1beta1.TaskLoopRunReason
		expectedTaskruns []*v1beta1.TaskRun
		expectedEvents   []string
	}{{
		name:             "Reconcile new taskloop run with a referenced task",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              runTaskLoop,
		taskruns:         []*v1beta1.TaskRun{},
		expectedStatus:   corev1.ConditionUnknown,
		expectedReason:   v1beta1.TaskLoopRunReasonRunning,
		expectedTaskruns: []*v1beta1.TaskRun{expectedTaskRunIteration1},
		expectedEvents:   []string{"Normal Started", "Normal Running Iterations completed: 0"},
	}, {
		name:             "Reconcile new taskloop run with an inline task",
		taskloop:         aTaskLoopWithInlineTask,
		run:              runTaskLoopWithInlineTask,
		taskruns:         []*v1beta1.TaskRun{},
		expectedStatus:   corev1.ConditionUnknown,
		expectedReason:   v1beta1.TaskLoopRunReasonRunning,
		expectedTaskruns: []*v1beta1.TaskRun{expectedTaskRunWithInlineTaskIteration1},
		expectedEvents:   []string{"Normal Started", "Normal Running Iterations completed: 0"},
	}, {
		name:             "Reconcile taskloop run after the first TaskRun succeeded.",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              loopRunning(runTaskLoop),
		taskruns:         []*v1beta1.TaskRun{successful(expectedTaskRunIteration1)},
		expectedStatus:   corev1.ConditionUnknown,
		expectedReason:   v1beta1.TaskLoopRunReasonRunning,
		expectedTaskruns: []*v1beta1.TaskRun{successful(expectedTaskRunIteration1), expectedTaskRunIteration2},
		expectedEvents:   []string{"Normal Running Iterations completed: 1"},
	}, {
		name:             "Reconcile taskloop run after all TaskRuns succeeded",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              loopRunning(runTaskLoop),
		taskruns:         []*v1beta1.TaskRun{successful(expectedTaskRunIteration1), successful(expectedTaskRunIteration2)},
		expectedStatus:   corev1.ConditionTrue,
		expectedReason:   v1beta1.TaskLoopRunReasonSucceeded,
		expectedTaskruns: []*v1beta1.TaskRun{successful(expectedTaskRunIteration1), successful(expectedTaskRunIteration2)},
		expectedEvents:   []string{"Normal Succeeded All TaskRuns completed successfully"},
	}, {
		name:             "Reconcile taskloop run after the first TaskRun failed",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              loopRunning(runTaskLoop),
		taskruns:         []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedStatus:   corev1.ConditionFalse,
		expectedReason:   v1beta1.TaskLoopRunReasonFailed,
		expectedTaskruns: []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedEvents:   []string{"Warning Failed TaskRun " + expectedTaskRunIteration1.Name + " has failed"},
	}, {
		name:             "Reconcile taskloop run after the first TaskRun failed and retry is allowed",
		task:             aTask,
		taskloop:         allowRetry(aTaskLoop),
		run:              loopRunning(runTaskLoop),
		taskruns:         []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedStatus:   corev1.ConditionUnknown,
		expectedReason:   v1beta1.TaskLoopRunReasonRunning,
		expectedTaskruns: []*v1beta1.TaskRun{retrying(failed(expectedTaskRunIteration1))},
		expectedEvents:   []string{},
	}, {
		name:             "Reconcile taskloop run after the first TaskRun failed and retry failed as well",
		task:             aTask,
		taskloop:         allowRetry(aTaskLoop),
		run:              loopRunning(runTaskLoop),
		taskruns:         []*v1beta1.TaskRun{failed(retrying(failed(expectedTaskRunIteration1)))},
		expectedStatus:   corev1.ConditionFalse,
		expectedReason:   v1beta1.TaskLoopRunReasonFailed,
		expectedTaskruns: []*v1beta1.TaskRun{failed(retrying(failed(expectedTaskRunIteration1)))},
		expectedEvents:   []string{"Warning Failed TaskRun " + expectedTaskRunIteration1.Name + " has failed"},
	}, {
		name:             "Reconcile cancelled taskloop run while the first TaskRun is running",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              requestCancel(loopRunning(runTaskLoop)),
		taskruns:         []*v1beta1.TaskRun{running(expectedTaskRunIteration1)},
		expectedStatus:   corev1.ConditionUnknown,
		expectedReason:   v1beta1.TaskLoopRunReasonRunning,
		expectedTaskruns: []*v1beta1.TaskRun{running(expectedTaskRunIteration1)},
		expectedEvents:   []string{"Normal Running Cancelling TaskRun " + expectedTaskRunIteration1.Name},
	}, {
		name:             "Reconcile cancelled taskloop run after the first TaskRun failed",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              requestCancel(loopRunning(runTaskLoop)),
		taskruns:         []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedStatus:   corev1.ConditionFalse,
		expectedReason:   v1beta1.TaskLoopRunReasonCancelled,
		expectedTaskruns: []*v1beta1.TaskRun{failed(expectedTaskRunIteration1)},
		expectedEvents:   []string{"Warning Failed TaskLoopRun " + runTaskLoop.Namespace + "/" + runTaskLoop.Name + " was cancelled"},
	}, {
		name:             "Reconcile cancelled taskloop run after the first TaskRun succeeded",
		task:             aTask,
		taskloop:         aTaskLoop,
		run:              requestCancel(loopRunning(runTaskLoop)),
		taskruns:         []*v1beta1.TaskRun{successful(expectedTaskRunIteration1)},
		expectedStatus:   corev1.ConditionFalse,
		expectedReason:   v1beta1.TaskLoopRunReasonCancelled,
		expectedTaskruns: []*v1beta1.TaskRun{successful(expectedTaskRunIteration1)},
		expectedEvents:   []string{"Warning Failed TaskLoopRun " + runTaskLoop.Namespace + "/" + runTaskLoop.Name + " was cancelled"},
	}}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			names.TestingSeed()

			optionalTask := []*v1beta1.Task{tc.task}
			if tc.task == nil {
				optionalTask = nil
			}

			d := test.Data{
				Tasks:        optionalTask,
				TaskLoops:    []*v1beta1.TaskLoop{tc.taskloop},
				TaskLoopRuns: []*v1beta1.TaskLoopRun{tc.run},
				TaskRuns:     tc.taskruns,
			}

			testAssets, _ := getTaskLoopRunController(t, d)
			c := testAssets.Controller
			clients := testAssets.Clients

			if err := c.Reconciler.Reconcile(context.Background(), getRunName(tc.run)); err != nil {
				t.Fatalf("Error reconciling: %s", err)
			}

			// Fetch the updated TaskLoopRun
			reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskLoopRuns(tc.run.Namespace).Get(tc.run.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Error getting reconciled run from fake client: %s", err)
			}

			// Verify that the TaskLoopRun has the expected status and reason.
			checkTaskLoopRunCondition(t, reconciledRun, tc.expectedStatus, tc.expectedReason)

			// Verify that a TaskRun was or was not created depending on the test.
			// If the number of expected TaskRuns is greater than the original number of TaskRuns
			// then the test expects a new TaskRun to be created.  The new TaskRun must be the
			// last one in the list of expected TaskRuns.
			createdTaskrun := getCreatedTaskrun(t, clients)
			if len(tc.expectedTaskruns) > len(tc.taskruns) {
				if createdTaskrun == nil {
					t.Errorf("A TaskRun should have been created but was not")
				} else {
					if d := cmp.Diff(tc.expectedTaskruns[len(tc.expectedTaskruns)-1], createdTaskrun); d != "" {
						t.Errorf("Expected TaskRun was not created. Diff %s", diff.PrintWantGot(d))
					}
				}
			} else {
				if createdTaskrun != nil {
					t.Errorf("A TaskRun was created which was not expected")
				}
			}

			// Verify TaskLoopRun status contains status for all TaskRuns.
			expectedTaskRuns := map[string]v1beta1.TaskLoopTaskRunStatus{}
			for i, tr := range tc.expectedTaskruns {
				expectedTaskRuns[tr.Name] = v1beta1.TaskLoopTaskRunStatus{Iteration: i + 1, Status: &tr.Status}
			}
			checkTaskLoopRunStatus(t, reconciledRun, expectedTaskRuns)

			// Verify expected events were created.
			if err := checkEvents(testAssets.Recorder, tc.name, tc.expectedEvents); err != nil {
				t.Errorf(err.Error())
			}
		})
	}
}

func TestReconcileTaskLoopRunFailures(t *testing.T) {
	testcases := []struct {
		name       string
		run        *v1beta1.TaskLoopRun
		reason     v1beta1.TaskLoopRunReason
		wantEvents []string
	}{{
		name:   "nonexistent TaskLoop",
		run:    runWithNonexistentTaskLoop,
		reason: v1beta1.TaskLoopRunReasonCouldntGetTaskLoop,
		wantEvents: []string{
			"Normal Started ",
			"Warning Failed Error retrieving TaskLoop",
		},
	}, {
		name:   "invalid timeout",
		run:    runTaskLoopWithInvalidTimeout,
		reason: v1beta1.TaskLoopRunReasonFailedValidation,
		wantEvents: []string{
			"Normal Started ",
			"Warning Failed The timeout value in TaskLoop foo/a-taskloop is not valid",
		},
	}, {
		name:   "negative timeout",
		run:    runTaskLoopWithNegativeTimeout,
		reason: v1beta1.TaskLoopRunReasonFailedValidation,
		wantEvents: []string{
			"Normal Started ",
			"Warning Failed The timeout value in TaskLoop foo/a-taskloop is negative",
		},
	}}

	d := test.Data{
		TaskLoops: []*v1beta1.TaskLoop{aTaskLoop},
		TaskLoopRuns: []*v1beta1.TaskLoopRun{
			runWithNonexistentTaskLoop, runTaskLoopWithInvalidTimeout, runTaskLoopWithNegativeTimeout},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			testAssets, _ := getTaskLoopRunController(t, d)
			c := testAssets.Controller
			clients := testAssets.Clients

			if err := c.Reconciler.Reconcile(context.Background(), getRunName(tc.run)); err != nil {
				t.Fatalf("Error reconciling: %s", err)
			}

			// Fetch the updated TaskLoopRun
			reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskLoopRuns(tc.run.Namespace).Get(tc.run.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Error getting reconciled run from fake client: %s", err)
			}

			// Verify that the TaskLoopRun is in Failed status and both the start time and the completion time are set.
			checkTaskLoopRunCondition(t, reconciledRun, corev1.ConditionFalse, tc.reason)
			if reconciledRun.Status.StartTime == nil {
				t.Fatalf("Expected TaskLoopRun start time to be set but it wasn't")
			}
			if reconciledRun.Status.CompletionTime == nil {
				t.Fatalf("Expected TaskLoopRun completion time to be set but it wasn't")
			}

			if err := checkEvents(testAssets.Recorder, tc.name, tc.wantEvents); err != nil {
				t.Errorf(err.Error())
			}
		})
	}
}
