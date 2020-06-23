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
	"fmt"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/substitution"
)

// ApplyParameters applies the params from a TaskLoopRun to a TaskLoopSpec
func ApplyParameters(spec *v1beta1.TaskLoopSpec, run *v1beta1.TaskLoopRun) *v1beta1.TaskLoopSpec {

	stringReplacements := map[string]string{}
	arrayReplacements := map[string][]string{}

	// Set all the default values from the declared parameters
	for _, p := range spec.Params {
		if p.Default != nil {
			if p.Default.Type == v1beta1.ParamTypeString {
				stringReplacements[fmt.Sprintf("params.%s", p.Name)] = p.Default.StringVal
			} else {
				arrayReplacements[fmt.Sprintf("params.%s", p.Name)] = p.Default.ArrayVal
			}
		}
	}
	// Set and overwrite params with the values from the run CRD
	for _, p := range run.Spec.Params {
		if p.Value.Type == v1beta1.ParamTypeString {
			stringReplacements[fmt.Sprintf("params.%s", p.Name)] = p.Value.StringVal
		} else {
			arrayReplacements[fmt.Sprintf("params.%s", p.Name)] = p.Value.ArrayVal
		}
	}

	spec = spec.DeepCopy()

	// Perform substitutions in task parameters
	for i := range spec.Task.Params {
		spec.Task.Params[i].Value.ApplyReplacements(stringReplacements, arrayReplacements)
	}

	// Perform substitutions in withItems
	var newItems []string
	for _, item := range spec.WithItems {
		newItems = append(newItems, substitution.ApplyArrayReplacements(item, stringReplacements, arrayReplacements)...)
	}
	spec.WithItems = newItems

	// Perform substitution for timeout
	if spec.Task.Timeout != "" {
		spec.Task.Timeout = substitution.ApplyReplacements(spec.Task.Timeout, stringReplacements)
	}

	return spec
}

// ApplyItem substitutes the $(item) reference in any task parameters
func ApplyItem(spec *v1beta1.TaskLoopSpec, item string) *v1beta1.TaskLoopSpec {
	stringReplacements := map[string]string{"item": item}

	spec = spec.DeepCopy()

	// Perform substitutions in task parameters
	for i := range spec.Task.Params {
		spec.Task.Params[i].Value.ApplyReplacements(stringReplacements, nil)
	}

	return spec
}
