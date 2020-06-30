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

package v1beta1_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/test/diff"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestTaskLoop_Validate_Success(t *testing.T) {
	tests := []struct {
		name string
		tl   *v1beta1.TaskLoop
	}{{
		name: "literal items",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1", "item2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "taskparameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
					}},
				},
			},
		},
	}, {
		name: "items passed in declared string parameters",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "item1",
					Type: v1beta1.ParamTypeString,
				}, {
					Name: "item2",
					Type: v1beta1.ParamTypeString,
				}},
				WithItems: []string{"$(params.item1)", "$(params.item2)"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "taskparameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
					}},
				},
			},
		},
	}, {
		name: "items passed in declared array parameter",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "items",
					Type: v1beta1.ParamTypeArray,
				}},
				WithItems: []string{"$(params.items)"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "taskparameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
					}},
				},
			},
		},
	}, {
		name: "expose task parameters as taskloop parameters",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "string-parameter",
					Type: v1beta1.ParamTypeString,
				}, {
					Name: "array-parameter",
					Type: v1beta1.ParamTypeArray,
				}},
				WithItems: []string{"run1", "run2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "string-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.string-parameter)"},
					}, {
						Name:  "array-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"$(params.array-parameter)"}},
					}},
				},
			},
		},
	}, {
		name: "literal task timeout",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1", "item2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Timeout: "10m",
				},
			},
		},
	}, {
		name: "parameterized task timeout",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "task-timeout",
					Type: v1beta1.ParamTypeString,
				}},
				WithItems: []string{"item1", "item2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Timeout: "$(params.task-timeout)",
				},
			},
		},
	}, {
		name: "taskSpec instead of taskRef",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1", "item2"},
				Task: v1beta1.TaskLoopTask{
					TaskSpec: &v1beta1.TaskSpec{
						Steps: []v1beta1.Step{{
							Container: corev1.Container{Name: "foo", Image: "bar"},
						}},
					},
				},
			},
		},
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tl.Validate(context.Background())
			if err != nil {
				t.Errorf("Unexpected error for %s: %s", tc.name, err)
			}
		})
	}
}

func TestTaskLoop_Validate_Error(t *testing.T) {
	tests := []struct {
		name          string
		tl            *v1beta1.TaskLoop
		expectedError apis.FieldError
	}{{
		name: "no taskRef or taskSpec",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1"},
			},
		},
		expectedError: apis.FieldError{
			Message: "expected exactly one, got neither",
			Paths:   []string{"spec.task.taskRef", "spec.task.taskSpec"},
		},
	}, {
		name: "both taskRef and taskSpec",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					TaskSpec: &v1beta1.TaskSpec{
						Steps: []v1beta1.Step{{
							Container: corev1.Container{Name: "foo", Image: "bar"},
						}},
					},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: "expected exactly one, got both",
			Paths:   []string{"spec.task.taskRef", "spec.task.taskSpec"},
		},
	}, {
		name: "invalid taskRef",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "_bad"},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: "invalid value: name part must consist of alphanumeric characters, '-', '_' or '.', and must start " +
				"and end with an alphanumeric character (e.g. 'MyName',  or 'my.name',  or '123-abc', regex used for " +
				"validation is '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]')",
			Paths: []string{"spec.task.taskRef.name"},
		},
	}, {
		name: "invalid taskSpec",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1", "item2"},
				Task: v1beta1.TaskLoopTask{
					TaskSpec: &v1beta1.TaskSpec{
						Steps: []v1beta1.Step{{
							Container: corev1.Container{Name: "bad@name!", Image: "bar"},
						}},
					},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `invalid value "bad@name!"`,
			Details: "Task step name must be a valid DNS Label, For more info refer to https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names",
			Paths:   []string{"spec.task.taskSpec.taskspec.steps.name"},
		},
	}, {
		name: "no items",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: "missing field(s)",
			Paths:   []string{"spec.withItems"},
		},
	}, {
		name: "items in undeclared parameter",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"$(params.undeclared-items)"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `non-existent variable in "$(params.undeclared-items)" for spec withItems[0]`,
			Paths:   []string{"spec.withItems[0]"},
		},
	}, {
		name: "items in non-isolated array parameter",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "items",
					Type: v1beta1.ParamTypeArray,
				}},
				WithItems: []string{"$(params.items),more"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `variable is not properly isolated in "$(params.items),more" for spec withItems[0]`,
			Paths:   []string{"spec.withItems[0]"},
		},
	}, {
		name: "task parameter value in undeclared taskloop parameter",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"run1"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "task-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.undeclared-parameter)"},
					}},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `non-existent variable in "$(params.undeclared-parameter)" for task parameter param[task-parameter]`,
			Paths:   []string{"spec.task.param[task-parameter]"},
		},
	}, {
		name: "array taskloop parameter used as task string parameter",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "array-parameter",
					Type: v1beta1.ParamTypeArray,
				}},
				WithItems: []string{"run1", "run2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "string-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.array-parameter)"},
					}},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `variable type invalid in "$(params.array-parameter)" for task parameter param[string-parameter]`,
			Paths:   []string{"spec.task.param[string-parameter]"},
		},
	}, {
		name: "non-isolated array parameter in task parameters",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "array-parameter",
					Type: v1beta1.ParamTypeArray,
				}},
				WithItems: []string{"run1", "run2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "array-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"$(params.array-parameter),more"}},
					}},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `variable is not properly isolated in "$(params.array-parameter),more" for task parameter param[array-parameter]`,
			Paths:   []string{"spec.task.param[array-parameter]"},
		},
	}, {
		name: "literal timeout not valid",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Timeout: "junk",
				},
			},
		},
		expectedError: apis.FieldError{
			Message: "invalid value: time: invalid duration junk",
			Paths:   []string{"spec.task.timeout"},
		},
	}, {
		name: "literal timeout is negative",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Timeout: "-1m",
				},
			},
		},
		expectedError: apis.FieldError{
			Message: "invalid value: the timeout value is negative",
			Paths:   []string{"spec.task.timeout"},
		},
	}, {
		name: "timeout in undeclared parameter",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Timeout: "$(params.undeclared-timeout)",
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `non-existent variable in "$(params.undeclared-timeout)" for task timeout`,
			Paths:   []string{"spec.task.timeout"},
		},
	}, {
		name: "timeout in array parameter",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "array-timeout",
					Type: v1beta1.ParamTypeArray,
				}},
				WithItems: []string{"item1"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "taskparameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
					}},
					Timeout: "$(params.array-timeout)",
				},
			},
		},
		expectedError: apis.FieldError{
			Message: `variable type invalid in "$(params.array-timeout)" for task timeout`,
			Paths:   []string{"spec.task.timeout"},
		},
	}, {
		name: "duplicated taskloop parameter declarations",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "dup-parameter",
					Type: v1beta1.ParamTypeString,
				}, {
					Name: "dup-parameter",
					Type: v1beta1.ParamTypeString,
				}},
				WithItems: []string{"run1", "run2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: "parameter is declared more than once",
			Paths:   []string{"spec.params.dup-parameter"},
		},
	}, {
		name: "duplicated task parameter settings",
		tl: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "task-parameter",
					Type: v1beta1.ParamTypeString,
				}},
				WithItems: []string{"run1", "run2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "task-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.task-parameter)"},
					}, {
						Name:  "task-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(params.task-parameter)"},
					}},
				},
			},
		},
		expectedError: apis.FieldError{
			Message: "task parameter appears more than once",
			Paths:   []string{"spec.task.params.task-parameter"},
		},
	}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.tl.Validate(context.Background())
			if err == nil {
				t.Errorf("Expected an Error but did not get one for %s", tc.name)
			} else {
				if d := cmp.Diff(tc.expectedError, *err, cmpopts.IgnoreUnexported(apis.FieldError{})); d != "" {
					t.Errorf("Error is different from expected for %s. diff %s", tc.name, diff.PrintWantGot(d))
				}
			}
		})
	}
}
