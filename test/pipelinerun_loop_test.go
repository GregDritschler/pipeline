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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/test/diff"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1beta1 "knative.dev/pkg/apis/duck/v1beta1"
	knativetest "knative.dev/pkg/test"
)

var (
	pipelinerunStartedEventMessage = "" // PipelineRun started event has no message
)

var aTaskUsedByPipeline = &v1beta1.Task{
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

var aPipeline = &v1beta1.Pipeline{
	ObjectMeta: metav1.ObjectMeta{
		Name: "a-pipeline",
		Labels: map[string]string{
			"myPipelineLabel": "myPipelineLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineAnnotation": "myPipelineAnnotationValue",
		},
	},
	Spec: v1beta1.PipelineSpec{
		Params: []v1beta1.ParamSpec{{
			Name: "items",
			Type: v1beta1.ParamTypeArray,
		}, {
			Name: "fail-on-item",
			Type: v1beta1.ParamTypeString,
		}},
		Tasks: []v1beta1.PipelineTask{{
			Name:    "loop",
			TaskRef: &v1beta1.TaskRef{Name: "a-task"},
			Params: []v1beta1.Param{{
				Name:  "current-item",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
			}, {
				Name:  "fail-on-item",
				Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.fail-on-item)"},
			}},
			WithItems: []string{"$(params.items)"},
		}},
	},
}

var runPipelineTaskLoopSuccess = &v1beta1.PipelineRun{
	ObjectMeta: metav1.ObjectMeta{
		Name: "run-pipelinetaskloop",
		Labels: map[string]string{
			"myPipelineRunLabel": "myPipelineRunLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineRunAnnotation": "myPipelineRunAnnotationValue",
		},
	},
	Spec: v1beta1.PipelineRunSpec{
		Params: []v1beta1.Param{{
			Name:  "items",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"item1", "item2"}},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "dontfailonanyitem"},
		}},
		PipelineRef: &v1beta1.PipelineRef{Name: "a-pipeline"},
	},
}

var runPipelineTaskLoopFailure = &v1beta1.PipelineRun{
	ObjectMeta: metav1.ObjectMeta{
		Name: "run-pipelinetaskloop",
		Labels: map[string]string{
			"myPipelineRunLabel": "myPipelineRunLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineRunAnnotation": "myPipelineRunAnnotationValue",
		},
	},
	Spec: v1beta1.PipelineRunSpec{
		Params: []v1beta1.Param{{
			Name:  "items",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"item1", "item2"}},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}},
		PipelineRef: &v1beta1.PipelineRef{Name: "a-pipeline"},
	},
}

var expectedParentTaskRunSuccess = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-pipelinetaskloop-loop-.*",
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/pipeline":          "a-pipeline",
			"tekton.dev/pipelineRun":       "run-pipelinetaskloop",
			"tekton.dev/pipelineTask":      "loop",
			"tekton.dev/task":              "a-task",
			"myPipelineRunLabel":           "myPipelineRunLabelValue",
			"myPipelineLabel":              "myPipelineLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineRunAnnotation": "myPipelineRunAnnotationValue",
			"myPipelineAnnotation":    "myPipelineAnnotationValue",
			"myTaskAnnotation":        "myTaskAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef:   &v1beta1.TaskRef{Name: "a-task", Kind: "Task"},
		Timeout:   &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
		Resources: &v1beta1.TaskRunResources{},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "dontfailonanyitem"},
		}},
		WithItems: []string{"item1", "item2"},
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

var expectedChildTaskRunIteration1Success = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-pipelinetaskloop-loop-.*-00001-.*",
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/parentTaskRun":     "run-pipelinetaskloop-loop-.*",
			"tekton.dev/pipeline":          "a-pipeline",
			"tekton.dev/pipelineRun":       "run-pipelinetaskloop",
			"tekton.dev/pipelineTask":      "loop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "1",
			"myPipelineLabel":              "myPipelineLabelValue",
			"myPipelineRunLabel":           "myPipelineRunLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineAnnotation":    "myPipelineAnnotationValue",
			"myPipelineRunAnnotation": "myPipelineRunAnnotationValue",
			"myTaskAnnotation":        "myTaskAnnotationValue",
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

var expectedChildTaskRunIteration2Success = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-pipelinetaskloop-loop-.*-00002-.*",
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/parentTaskRun":     "run-pipelinetaskloop-loop-.*",
			"tekton.dev/pipeline":          "a-pipeline",
			"tekton.dev/pipelineRun":       "run-pipelinetaskloop",
			"tekton.dev/pipelineTask":      "loop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "2",
			"myPipelineLabel":              "myPipelineLabelValue",
			"myPipelineRunLabel":           "myPipelineRunLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineAnnotation":    "myPipelineAnnotationValue",
			"myPipelineRunAnnotation": "myPipelineRunAnnotationValue",
			"myTaskAnnotation":        "myTaskAnnotationValue",
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

var expectedParentTaskRunFailure = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-pipelinetaskloop-loop-.*",
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/pipeline":          "a-pipeline",
			"tekton.dev/pipelineRun":       "run-pipelinetaskloop",
			"tekton.dev/pipelineTask":      "loop",
			"tekton.dev/task":              "a-task",
			"myPipelineLabel":              "myPipelineLabelValue",
			"myPipelineRunLabel":           "myPipelineRunLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineAnnotation":    "myPipelineAnnotationValue",
			"myPipelineRunAnnotation": "myPipelineRunAnnotationValue",
			"myTaskAnnotation":        "myTaskAnnotationValue",
		},
	},
	Spec: v1beta1.TaskRunSpec{
		TaskRef:   &v1beta1.TaskRef{Name: "a-task", Kind: "Task"},
		Timeout:   &metav1.Duration{Duration: 1 * time.Hour}, // default TaskRun timeout
		Resources: &v1beta1.TaskRunResources{},
		Params: []v1beta1.Param{{
			Name:  "current-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
		}, {
			Name:  "fail-on-item",
			Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
		}},
		WithItems: []string{"item1", "item2"},
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

var expectedChildTaskRunIteration1Failure = &v1beta1.TaskRun{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "run-pipelinetaskloop-loop-.*-00001-.*",
		Namespace: "foo",
		Labels: map[string]string{
			"app.kubernetes.io/managed-by": "tekton-pipelines",
			"tekton.dev/parentTaskRun":     "run-pipelinetaskloop-loop-.*",
			"tekton.dev/pipeline":          "a-pipeline",
			"tekton.dev/pipelineRun":       "run-pipelinetaskloop",
			"tekton.dev/pipelineTask":      "loop",
			"tekton.dev/task":              "a-task",
			"tekton.dev/taskLoopIteration": "1",
			"myPipelineLabel":              "myPipelineLabelValue",
			"myPipelineRunLabel":           "myPipelineRunLabelValue",
			"myTaskLabel":                  "myTaskLabelValue",
		},
		Annotations: map[string]string{
			"myPipelineAnnotation":    "myPipelineAnnotationValue",
			"myPipelineRunAnnotation": "myPipelineRunAnnotationValue",
			"myTaskAnnotation":        "myTaskAnnotationValue",
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

func TestLoopingPipelineRun(t *testing.T) {
	t.Parallel()

	testcases := []struct {
		name string
		// The following set of fields describe the resources to create.
		task        *v1beta1.Task
		pipeline    *v1beta1.Pipeline
		pipelineRun *v1beta1.PipelineRun
		// The following set of fields describe the expected outcome.
		expectedPipelineRunStatus corev1.ConditionStatus
		expectedPipelineRunReason v1beta1.PipelineRunReason
		expectedParentTaskRun     *v1beta1.TaskRun
		expectedChildTaskRuns     []*v1beta1.TaskRun
		expectedEvents            []string
	}{{
		name:                      "looping Pipeline task succeeds",
		task:                      aTaskUsedByPipeline,
		pipeline:                  aPipeline,
		pipelineRun:               runPipelineTaskLoopSuccess,
		expectedPipelineRunStatus: corev1.ConditionTrue,
		expectedPipelineRunReason: v1beta1.PipelineRunReasonSuccessful,
		expectedParentTaskRun:     expectedParentTaskRunSuccess,
		expectedChildTaskRuns:     []*v1beta1.TaskRun{expectedChildTaskRunIteration1Success, expectedChildTaskRunIteration2Success},
		expectedEvents:            []string{pipelinerunStartedEventMessage, "Iterations completed: 0", "Iterations completed: 1", "All iterations completed successfully"},
	}, {
		name:                      "looping Pipeline task fails",
		task:                      aTaskUsedByPipeline,
		pipeline:                  aPipeline,
		pipelineRun:               runPipelineTaskLoopFailure,
		expectedPipelineRunStatus: corev1.ConditionFalse,
		expectedPipelineRunReason: v1beta1.PipelineRunReasonFailed,
		expectedParentTaskRun:     expectedParentTaskRunFailure,
		expectedChildTaskRuns:     []*v1beta1.TaskRun{expectedChildTaskRunIteration1Failure},
		expectedEvents:            []string{pipelinerunStartedEventMessage, "Iterations completed: 0", "TaskRun run-pipelinetaskloop-loop-.*-00001-.* has failed"},
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

			if _, err := c.PipelineClient.Create(tc.pipeline); err != nil {
				t.Fatalf("Failed to create Pipeline `%s`: %s", tc.pipeline.Name, err)
			}

			pipelineRun, err := c.PipelineRunClient.Create(tc.pipelineRun)
			if err != nil {
				t.Fatalf("Failed to create PipelineRun `%s`: %s", tc.pipelineRun.Name, err)
			}

			t.Logf("Waiting for PipelineRun %s/%s to complete", pipelineRun.Namespace, pipelineRun.Name)
			var inState ConditionAccessorFn
			var desc string
			if tc.expectedPipelineRunStatus == corev1.ConditionTrue {
				inState = Succeed(pipelineRun.Name)
				desc = "PipelineRunSuccess"
			} else {
				inState = FailedWithReason(tc.expectedPipelineRunReason.String(), pipelineRun.Name)
				desc = "PipelineRunFailed"
			}
			if err := WaitForPipelineRunState(c, pipelineRun.Name, pipelineRunTimeout, inState, desc); err != nil {
				t.Fatalf("Error waiting for PipelineRun %s/%s to finish: %s", pipelineRun.Namespace, pipelineRun.Name, err)
			}

			pipelineRun, err = c.PipelineRunClient.Get(pipelineRun.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Couldn't get reconciled parent PipelineRun %s/%s: %s", pipelineRun.Namespace, pipelineRun.Name, err)
			}

			t.Logf("Making sure the expected parent TaskRun was created")
			parentTaskRunName := ""
			for k, _ := range pipelineRun.Status.TaskRuns {
				parentTaskRunName = k
			}
			parentTaskRun, err := c.TaskRunClient.Get(parentTaskRunName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("Couldn't get parent TaskRun %s: %s", parentTaskRunName, err)
			}
			checkTaskRun(t, tc.expectedParentTaskRun, parentTaskRun)

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
					if matched, _ := regexp.MatchString(expectedTaskRun.Name, actualTaskRun.Name); matched {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected child TaskRun with prefix %s for parent TaskRun %s/%s not found",
						expectedTaskRun.Name, parentTaskRun.Namespace, parentTaskRun.Name)
					continue
				}
				checkTaskRun(t, expectedTaskRun, &actualTaskRun)

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
