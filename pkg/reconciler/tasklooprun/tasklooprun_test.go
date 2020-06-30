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

func verifyTaskLoopRunCondition(t *testing.T, tlr *v1beta1.TaskLoopRun, expectedStatus corev1.ConditionStatus, expectedReason v1beta1.TaskLoopRunReason) {
	condition := tlr.Status.GetCondition(apis.ConditionSucceeded)
	if condition == nil || condition.Status != expectedStatus {
		t.Errorf("Expected TaskLoopRun status to be %v but was %v", expectedStatus, condition)
	}
	if condition != nil && condition.Reason != expectedReason.String() {
		t.Errorf("Expected reason %q but was %s", expectedReason.String(), condition.Reason)
	}
}

func verifyTaskLoopRunStatus(t *testing.T, tlr *v1beta1.TaskLoopRun, expectedStatus map[string]v1beta1.TaskLoopTaskRunStatus) {
	// TODO: I will need to use Run function getAdditionalFields("taskruns")
	t.Log("taskruns", tlr.Status.TaskRuns)
	if len(tlr.Status.TaskRuns) != len(expectedStatus) {
		t.Errorf("Expected TaskLoopRun status to include two TaskRuns: %v", tlr.Status.TaskRuns)
	}
	for expectedTaskRunName, expectedTaskRunStatus := range expectedStatus {
		actualTaskRunStatus, exists := tlr.Status.TaskRuns[expectedTaskRunName]
		if !exists {
			t.Errorf("Expected TaskLoopRun status to include TaskRun status for TaskRun %s", expectedTaskRunName)
		}
		if actualTaskRunStatus.Iteration != expectedTaskRunStatus.Iteration {
			t.Errorf("TaskLoopRun status for TaskRun %s has iteration number %d instead of %d",
				expectedTaskRunName, actualTaskRunStatus.Iteration, expectedTaskRunStatus.Iteration)
		}
		if d := cmp.Diff(expectedTaskRunStatus.Status, actualTaskRunStatus.Status); d != "" {
			t.Errorf("TaskLoopRun status for TaskRun %s is incorrect. Diff %s", expectedTaskRunName, diff.PrintWantGot(d))
		}
	}
}

var basicTask = &v1beta1.Task{
	ObjectMeta: metav1.ObjectMeta{Name: "basic-task", Namespace: "foo"},
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

var basicTaskLoop = &v1beta1.TaskLoop{
	ObjectMeta: metav1.ObjectMeta{Name: "basic-taskloop", Namespace: "foo"},
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
			TaskRef: &v1beta1.TaskRef{Name: "basic-task"},
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

var basicTaskLoopRun = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "basic-tasklooprun",
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
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "basic-taskloop"},
	},
}

var badTaskLoopRunTaskLoopNotFound = &v1beta1.TaskLoopRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "bad-tasklooprun-taskloop-not-found",
		Namespace: "foo",
	},
	Spec: v1beta1.TaskLoopRunSpec{
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "no-such-taskloop"},
	},
}

var badTaskLoopRunInvalidTimeoutParameter = &v1beta1.TaskLoopRun{
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
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "basic-taskloop"},
	},
}

var badTaskLoopRunNegativeTimeoutParameter = &v1beta1.TaskLoopRun{
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
		TaskLoopRef: &v1beta1.TaskLoopRef{Name: "basic-taskloop"},
	},
}

var expectedTaskRunIteration1 = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "basic-tasklooprun-00001-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskLoopRun",
			Name:               "basic-tasklooprun",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
			"tekton.dev/taskLoop":          "basic-taskloop",
			"tekton.dev/taskLoopRun":       "basic-tasklooprun",
			"tekton.dev/taskLoopIteration": "1",
			"myTestLabel":                  "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef: &v1beta1.TaskRef{Name: "basic-task"},
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
		Name:      "basic-tasklooprun-00002-9l9zj",
		Namespace: "foo",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion:         "tekton.dev/v1beta1",
			Kind:               "TaskLoopRun",
			Name:               "basic-tasklooprun",
			Controller:         &trueB,
			BlockOwnerDeletion: &trueB,
		}},
		Labels: map[string]string{
			"tekton.dev/taskLoop":          "basic-taskloop",
			"tekton.dev/taskLoopRun":       "basic-tasklooprun",
			"tekton.dev/taskLoopIteration": "2",
			"myTestLabel":                  "myTestLabelValue",
		},
		Annotations: map[string]string{
			"myTestAnnotation": "myTestAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef: &v1beta1.TaskRef{Name: "basic-task"},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item2"},
		}, {
			Name:  "additional-parameter",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "stuff"},
		}},
	},
}

func TestReconcileNewTaskLoop(t *testing.T) {
	names.TestingSeed()

	d := test.Data{
		Tasks:        []*v1beta1.Task{basicTask},
		TaskLoops:    []*v1beta1.TaskLoop{basicTaskLoop},
		TaskLoopRuns: []*v1beta1.TaskLoopRun{basicTaskLoopRun},
	}

	testAssets, _ := getTaskLoopRunController(t, d)
	c := testAssets.Controller
	clients := testAssets.Clients

	if err := c.Reconciler.Reconcile(context.Background(), "foo/basic-tasklooprun"); err != nil {
		t.Fatalf("Error reconciling: %s", err)
	}

	// Fetch the updated TaskLoopRun
	reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskLoopRuns("foo").Get("basic-tasklooprun", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Error getting reconciled run from fake client: %s", err)
	}

	// Verify that the TaskLoopRun is in Running status, the start time is set, and the completion time is not set.
	verifyTaskLoopRunCondition(t, reconciledRun, corev1.ConditionUnknown, v1beta1.TaskLoopRunReasonRunning)
	if reconciledRun.Status.StartTime == nil {
		t.Fatalf("Expected TaskLoopRun start time to be set but it wasn't")
	}
	if reconciledRun.Status.CompletionTime != nil {
		t.Fatalf("Expected TaskLoopRun completion time to not be set but it was")
	}

	// Verify that a TaskRun was created for the first iteration.
	if len(clients.Pipeline.Actions()) == 0 {
		t.Fatalf("Expected client to have been used to create a TaskRun but it wasn't")
	}
	t.Log("actions", clients.Pipeline.Actions())
	actual := clients.Pipeline.Actions()[0].(ktesting.CreateAction).GetObject()
	if d := cmp.Diff(expectedTaskRunIteration1, actual); d != "" {
		t.Errorf("Expected TaskRun was not created. Diff %s", diff.PrintWantGot(d))
	}

	// Verify TaskLoopRun status contains status for expected TaskRun.
	verifyTaskLoopRunStatus(t, reconciledRun, map[string]v1beta1.TaskLoopTaskRunStatus{
		"basic-tasklooprun-00001-9l9zj": v1beta1.TaskLoopTaskRunStatus{Iteration: 1, Status: &v1beta1.TaskRunStatus{}},
	})

	wantEvents := []string{
		"Normal Started ",
		"Normal Running Iterations completed: 0",
	}
	if err := checkEvents(testAssets.Recorder, "new TaskLoop", wantEvents); err != nil {
		t.Errorf(err.Error())
	}
}

func TestReconcileTaskLoopAfterFirstIterationIsSuccessful(t *testing.T) {
	names.TestingSeed()

	// Mark the TaskLoopRun started.
	basicTaskLoopRunWithStatus := basicTaskLoopRun.DeepCopy()
	basicTaskLoopRunWithStatus.Status.InitializeConditions()
	// Mark the first TaskRun completed.
	tr := expectedTaskRunIteration1.DeepCopy()
	tr.Status.SetCondition(&apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionTrue,
		Reason:  v1beta1.TaskRunReasonSuccessful.String(),
		Message: "All Steps have completed executing",
	})

	d := test.Data{
		Tasks:        []*v1beta1.Task{basicTask},
		TaskLoops:    []*v1beta1.TaskLoop{basicTaskLoop},
		TaskLoopRuns: []*v1beta1.TaskLoopRun{basicTaskLoopRunWithStatus},
		TaskRuns:     []*v1beta1.TaskRun{tr},
	}

	testAssets, _ := getTaskLoopRunController(t, d)
	c := testAssets.Controller
	clients := testAssets.Clients

	if err := c.Reconciler.Reconcile(context.Background(), "foo/basic-tasklooprun"); err != nil {
		t.Fatalf("Error reconciling: %s", err)
	}

	// Fetch the updated TaskLoopRun
	reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskLoopRuns("foo").Get("basic-tasklooprun", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Error getting reconciled run from fake client: %s", err)
	}

	// Verify that the TaskLoopRun is in Running status, the start time is set, and the completion time is not set.
	verifyTaskLoopRunCondition(t, reconciledRun, corev1.ConditionUnknown, v1beta1.TaskLoopRunReasonRunning)
	if reconciledRun.Status.StartTime == nil {
		t.Fatalf("Expected TaskLoopRun start time to be set but it wasn't")
	}
	if reconciledRun.Status.CompletionTime != nil {
		t.Fatalf("Expected TaskLoopRun completion time to not be set but it was")
	}

	// Verify that a TaskRun was created for the second iteration.
	if len(clients.Pipeline.Actions()) == 0 {
		t.Fatalf("Expected client to have been used to create a TaskRun but it wasn't")
	}
	t.Log("actions", clients.Pipeline.Actions())
	actual := clients.Pipeline.Actions()[0].(ktesting.CreateAction).GetObject()
	if d := cmp.Diff(expectedTaskRunIteration2, actual); d != "" {
		t.Errorf("Expected TaskRun was not created. Diff %s", diff.PrintWantGot(d))
	}

	// Verify TaskLoopRun status contains status for expected TaskRuns.
	verifyTaskLoopRunStatus(t, reconciledRun, map[string]v1beta1.TaskLoopTaskRunStatus{
		"basic-tasklooprun-00001-9l9zj": v1beta1.TaskLoopTaskRunStatus{Iteration: 1, Status: &tr.Status},
		"basic-tasklooprun-00002-9l9zj": v1beta1.TaskLoopTaskRunStatus{Iteration: 2, Status: &v1beta1.TaskRunStatus{}},
	})

	wantEvents := []string{
		"Normal Running Iterations completed: 1",
	}
	if err := checkEvents(testAssets.Recorder, "first iteration successful", wantEvents); err != nil {
		t.Errorf(err.Error())
	}
}

func TestReconcileTaskLoopAfterLastIterationIsSuccessful(t *testing.T) {
	names.TestingSeed()

	// Mark the TaskLoopRun started.
	basicTaskLoopRunWithStatus := basicTaskLoopRun.DeepCopy()
	basicTaskLoopRunWithStatus.Status.InitializeConditions()
	// Mark the first TaskRun completed.
	tr := expectedTaskRunIteration1.DeepCopy()
	tr.Status.SetCondition(&apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionTrue,
		Reason:  v1beta1.TaskRunReasonSuccessful.String(),
		Message: "All Steps have completed executing",
	})
	// Mark the second TaskRun completed.
	tr2 := expectedTaskRunIteration2.DeepCopy()
	tr2.Status.SetCondition(&apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionTrue,
		Reason:  v1beta1.TaskRunReasonSuccessful.String(),
		Message: "All Steps have completed executing",
	})

	d := test.Data{
		Tasks:        []*v1beta1.Task{basicTask},
		TaskLoops:    []*v1beta1.TaskLoop{basicTaskLoop},
		TaskLoopRuns: []*v1beta1.TaskLoopRun{basicTaskLoopRunWithStatus},
		TaskRuns:     []*v1beta1.TaskRun{tr, tr2},
	}

	testAssets, _ := getTaskLoopRunController(t, d)
	c := testAssets.Controller
	clients := testAssets.Clients

	if err := c.Reconciler.Reconcile(context.Background(), "foo/basic-tasklooprun"); err != nil {
		t.Fatalf("Error reconciling: %s", err)
	}

	// Fetch the updated TaskLoopRun
	reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskLoopRuns("foo").Get("basic-tasklooprun", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Error getting reconciled run from fake client: %s", err)
	}

	// Verify that the TaskLoopRun is in Complete status and the start and completion times are set.
	verifyTaskLoopRunCondition(t, reconciledRun, corev1.ConditionTrue, v1beta1.TaskLoopRunReasonSucceeded)
	if reconciledRun.Status.StartTime == nil {
		t.Fatalf("Expected TaskLoopRun start time to be set but it wasn't")
	}
	if reconciledRun.Status.CompletionTime == nil {
		t.Fatalf("Expected TaskLoopRun completion time to be set but it wasn't")
	}

	// Verify TaskLoopRun status contains status for expected TaskRuns.
	verifyTaskLoopRunStatus(t, reconciledRun, map[string]v1beta1.TaskLoopTaskRunStatus{
		"basic-tasklooprun-00001-9l9zj": v1beta1.TaskLoopTaskRunStatus{Iteration: 1, Status: &tr.Status},
		"basic-tasklooprun-00002-9l9zj": v1beta1.TaskLoopTaskRunStatus{Iteration: 2, Status: &tr2.Status},
	})

	wantEvents := []string{
		"Normal Succeeded All TaskRuns completed successfully",
	}
	if err := checkEvents(testAssets.Recorder, "last iteration successful", wantEvents); err != nil {
		t.Errorf(err.Error())
	}
}

func TestReconcileTaskLoopAfterFirstIterationFails(t *testing.T) {
	names.TestingSeed()

	// Mark the TaskLoopRun started.
	basicTaskLoopRunWithStatus := basicTaskLoopRun.DeepCopy()
	basicTaskLoopRunWithStatus.Status.InitializeConditions()
	// Mark the first TaskRun failed.
	tr := expectedTaskRunIteration1.DeepCopy()
	tr.Status.SetCondition(&apis.Condition{
		Type:    apis.ConditionSucceeded,
		Status:  corev1.ConditionFalse,
		Reason:  v1beta1.TaskRunReasonFailed.String(),
		Message: "Something went wrong",
	})

	d := test.Data{
		Tasks:        []*v1beta1.Task{basicTask},
		TaskLoops:    []*v1beta1.TaskLoop{basicTaskLoop},
		TaskLoopRuns: []*v1beta1.TaskLoopRun{basicTaskLoopRunWithStatus},
		TaskRuns:     []*v1beta1.TaskRun{tr},
	}

	testAssets, _ := getTaskLoopRunController(t, d)
	c := testAssets.Controller
	clients := testAssets.Clients

	if err := c.Reconciler.Reconcile(context.Background(), "foo/basic-tasklooprun"); err != nil {
		t.Fatalf("Error reconciling: %s", err)
	}

	// Fetch the updated TaskLoopRun
	reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskLoopRuns("foo").Get("basic-tasklooprun", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Error getting reconciled run from fake client: %s", err)
	}

	// Verify that the TaskLoopRun is in Failed status and both the start time and the completion time are set.
	verifyTaskLoopRunCondition(t, reconciledRun, corev1.ConditionFalse, v1beta1.TaskLoopRunReasonFailed)
	if reconciledRun.Status.StartTime == nil {
		t.Fatalf("Expected TaskLoopRun start time to be set but it wasn't")
	}
	if reconciledRun.Status.CompletionTime == nil {
		t.Fatalf("Expected TaskLoopRun completion time to be set but it wasn't")
	}

	// Verify TaskLoopRun status contains status for expected TaskRun.
	verifyTaskLoopRunStatus(t, reconciledRun, map[string]v1beta1.TaskLoopTaskRunStatus{
		"basic-tasklooprun-00001-9l9zj": v1beta1.TaskLoopTaskRunStatus{Iteration: 1, Status: &tr.Status},
	})

	wantEvents := []string{
		"Warning Failed TaskRun basic-tasklooprun-00001-9l9zj has failed",
	}
	if err := checkEvents(testAssets.Recorder, "last iteration successful", wantEvents); err != nil {
		t.Errorf(err.Error())
	}
}

func TestReconcileTaskLoopRunFailures(t *testing.T) {
	testcases := []struct {
		name       string
		tlr        *v1beta1.TaskLoopRun
		reason     v1beta1.TaskLoopRunReason
		wantEvents []string
	}{{
		name:   "TaskLoop not found",
		tlr:    badTaskLoopRunTaskLoopNotFound,
		reason: v1beta1.TaskLoopRunReasonCouldntGetTaskLoop,
		wantEvents: []string{
			"Normal Started ",
			"Warning Failed Error retrieving TaskLoop",
		},
	}, {
		name:   "invalid timeout",
		tlr:    badTaskLoopRunInvalidTimeoutParameter,
		reason: v1beta1.TaskLoopRunReasonFailedValidation,
		wantEvents: []string{
			"Normal Started ",
			"Warning Failed The timeout value in TaskLoop foo/basic-taskloop is not valid",
		},
	}, {
		name:   "negative timeout",
		tlr:    badTaskLoopRunNegativeTimeoutParameter,
		reason: v1beta1.TaskLoopRunReasonFailedValidation,
		wantEvents: []string{
			"Normal Started ",
			"Warning Failed The timeout value in TaskLoop foo/basic-taskloop is negative",
		},
	}}

	d := test.Data{
		TaskLoops: []*v1beta1.TaskLoop{basicTaskLoop},
		TaskLoopRuns: []*v1beta1.TaskLoopRun{
			badTaskLoopRunTaskLoopNotFound, badTaskLoopRunInvalidTimeoutParameter, badTaskLoopRunNegativeTimeoutParameter},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			testAssets, _ := getTaskLoopRunController(t, d)
			c := testAssets.Controller
			clients := testAssets.Clients

			if err := c.Reconciler.Reconcile(context.Background(), getRunName(tc.tlr)); err != nil {
				t.Fatalf("Error reconciling: %s", err)
			}

			// Fetch the updated TaskLoopRun
			reconciledRun, err := clients.Pipeline.TektonV1beta1().TaskLoopRuns(tc.tlr.Namespace).Get(tc.tlr.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Error getting reconciled run from fake client: %s", err)
			}

			// Verify that the TaskLoopRun is in Failed status and both the start time and the completion time are set.
			verifyTaskLoopRunCondition(t, reconciledRun, corev1.ConditionFalse, tc.reason)
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
