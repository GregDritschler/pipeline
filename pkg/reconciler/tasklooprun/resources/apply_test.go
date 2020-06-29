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

package resources

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/test/diff"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyParameters(t *testing.T) {
	tests := []struct {
		name     string
		original *v1beta1.TaskLoop
		run      *v1beta1.TaskLoopRun
		expected *v1beta1.TaskLoop
	}{{
		name: "task parameter substitution",
		original: &v1beta1.TaskLoop{
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
		run: &v1beta1.TaskLoopRun{
			ObjectMeta: metav1.ObjectMeta{Name: "tasklooprun"},
			Spec: v1beta1.TaskLoopRunSpec{
				Params: []v1beta1.Param{{
					Name:  "string-parameter",
					Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "string-value"},
				}, {
					Name:  "array-parameter",
					Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"element1", "element2"}},
				}},
				TaskLoopRef: &v1beta1.TaskLoopRef{Name: "taskloop"},
			},
		},
		expected: &v1beta1.TaskLoop{
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
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "string-value"},
					}, {
						Name:  "array-parameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"element1", "element2"}},
					}},
				},
			},
		},
	}, {
		name: "withItems substitution",
		original: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "array-items",
					Type: v1beta1.ParamTypeArray,
				}, {
					Name: "string-item",
					Type: v1beta1.ParamTypeString,
				}},
				WithItems: []string{"$(params.array-items)", "$(params.string-item)", "item4"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "taskparameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "$(item)"},
					}},
				},
			},
		},
		run: &v1beta1.TaskLoopRun{
			ObjectMeta: metav1.ObjectMeta{Name: "tasklooprun"},
			Spec: v1beta1.TaskLoopRunSpec{
				Params: []v1beta1.Param{{
					Name:  "array-items",
					Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeArray, ArrayVal: []string{"item1", "item2"}},
				}, {
					Name:  "string-item",
					Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item3"},
				}},
				TaskLoopRef: &v1beta1.TaskLoopRef{Name: "taskloop"},
			},
		},
		expected: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "array-items",
					Type: v1beta1.ParamTypeArray,
				}, {
					Name: "string-item",
					Type: v1beta1.ParamTypeString,
				}},
				WithItems: []string{"item1", "item2", "item3", "item4"},
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
		name: "task timeout substitution",
		original: &v1beta1.TaskLoop{
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
		run: &v1beta1.TaskLoopRun{
			ObjectMeta: metav1.ObjectMeta{Name: "tasklooprun"},
			Spec: v1beta1.TaskLoopRunSpec{
				Params: []v1beta1.Param{{
					Name:  "task-timeout",
					Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "10m"},
				}},
				TaskLoopRef: &v1beta1.TaskLoopRef{Name: "taskloop"},
			},
		},
		expected: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				Params: []v1beta1.ParamSpec{{
					Name: "task-timeout",
					Type: v1beta1.ParamTypeString,
				}},
				WithItems: []string{"item1", "item2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Timeout: "10m",
				},
			},
		},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyParameters(&tt.original.Spec, tt.run)
			if d := cmp.Diff(&tt.expected.Spec, got); d != "" {
				t.Errorf("ApplyParameters() returned unexpected result for %s: diff %s", tt.name, diff.PrintWantGot(d))
			}
		})
	}
}

func TestApplyItem(t *testing.T) {
	tests := []struct {
		name     string
		original *v1beta1.TaskLoop
		item     string
		expected *v1beta1.TaskLoop
	}{{
		name: "current item substitution",
		original: &v1beta1.TaskLoop{
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
		item: "item1",
		expected: &v1beta1.TaskLoop{
			ObjectMeta: metav1.ObjectMeta{Name: "taskloop"},
			Spec: v1beta1.TaskLoopSpec{
				WithItems: []string{"item1", "item2"},
				Task: v1beta1.TaskLoopTask{
					TaskRef: &v1beta1.TaskRef{Name: "mytask"},
					Params: []v1beta1.Param{{
						Name:  "taskparameter",
						Value: v1beta1.ArrayOrString{Type: v1beta1.ParamTypeString, StringVal: "item1"},
					}},
				},
			},
		},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyItem(&tt.original.Spec, tt.item)
			if d := cmp.Diff(&tt.expected.Spec, got); d != "" {
				t.Errorf("ApplyItem() returned unexpected result for %s: diff %s", tt.name, diff.PrintWantGot(d))
			}
		})
	}
}
