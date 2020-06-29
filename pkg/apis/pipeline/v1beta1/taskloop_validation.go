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

package v1beta1

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tektoncd/pipeline/pkg/apis/validate"
	"github.com/tektoncd/pipeline/pkg/substitution"
	"k8s.io/apimachinery/pkg/util/validation"
	"knative.dev/pkg/apis"
)

var _ apis.Validatable = (*TaskLoop)(nil)

// Validate TaskLoop
func (tl *TaskLoop) Validate(ctx context.Context) *apis.FieldError {
	if err := validate.ObjectMetadata(tl.GetObjectMeta()); err != nil {
		return err.ViaField("metadata")
	}
	return tl.Spec.Validate(ctx)
}

// Validate TaskLoopSpec
func (tls *TaskLoopSpec) Validate(ctx context.Context) *apis.FieldError {
	// Require at least one item to process.
	if len(tls.WithItems) == 0 {
		return apis.ErrMissingField("spec.withItems")
	}
	// Validate that the declared parameter types are correct.
	if err := ValidateParameterTypes(tls.Params); err != nil {
		return err
	}
	// Validate Task reference or inline task spec.
	if err := validateTask(ctx, tls); err != nil {
		return err
	}
	// Validate usage of variables.
	if err := validateTaskLoopVariables(tls); err != nil {
		return err
	}
	// Validate timeout field value if it is not parameterized in any way.
	if tls.Task.Timeout != "" && !strings.Contains(tls.Task.Timeout, "$(params") {
		if _, err := time.ParseDuration(tls.Task.Timeout); err != nil {
			return apis.ErrInvalidValue(err.Error(), "spec.task.timeout")
		}
	}
	return nil
}

func validateTask(ctx context.Context, tls *TaskLoopSpec) *apis.FieldError {
	// taskRef and taskSpec are mutually exclusive.
	if (tls.Task.TaskRef != nil && tls.Task.TaskRef.Name != "") && tls.Task.TaskSpec != nil {
		return apis.ErrMultipleOneOf("spec.task.taskRef", "spec.task.taskSpec")
	}
	// Check that one of taskRef and taskSpec is present.
	if (tls.Task.TaskRef == nil || tls.Task.TaskRef.Name == "") && tls.Task.TaskSpec == nil {
		return apis.ErrMissingOneOf("spec.task.taskRef", "spec.task.taskSpec")
	}
	// Validate TaskSpec if it's present
	if tls.Task.TaskSpec != nil {
		if err := tls.Task.TaskSpec.Validate(ctx); err != nil {
			return err.ViaField("spec.task.taskSpec")
		}
	}
	if tls.Task.TaskRef != nil && tls.Task.TaskRef.Name != "" {
		// taskRef name must be a valid k8s name
		if errSlice := validation.IsQualifiedName(tls.Task.TaskRef.Name); len(errSlice) != 0 {
			return apis.ErrInvalidValue(strings.Join(errSlice, ","), "spec.task.taskRef.name")
		}
	}
	return nil
}

func validateTaskLoopVariables(tls *TaskLoopSpec) *apis.FieldError {
	const prefix = "params"
	parameterNames := map[string]struct{}{}
	arrayParameterNames := map[string]struct{}{}

	// Gather declared parameter names.
	for _, p := range tls.Params {
		if _, ok := parameterNames[p.Name]; ok {
			return apis.ErrGeneric("parameter is declared more than once", fmt.Sprintf("spec.params.%s", p.Name))
		}
		parameterNames[p.Name] = struct{}{}
		if p.Type == ParamTypeArray {
			arrayParameterNames[p.Name] = struct{}{}
		}
	}

	// Validate any parameter references in withItems are declared and array references are isolated.
	for i, item := range tls.WithItems {
		if err := substitution.ValidateVariable(fmt.Sprintf("withItems[%d]", i), item, prefix, "spec", "spec", parameterNames); err != nil {
			return err
		}
		if err := substitution.ValidateVariableIsolated(fmt.Sprintf("withItems[%d]", i), item, prefix, "spec", "spec", arrayParameterNames); err != nil {
			return err
		}
	}

	// Validate any parameter reference in task timeout is declared and is not an array parameter.
	if err := substitution.ValidateVariable("timeout", tls.Task.Timeout, prefix, "task", "spec.task", parameterNames); err != nil {
		return err
	}
	if err := substitution.ValidateVariableProhibited("timeout", tls.Task.Timeout, prefix, "task", "spec.task", arrayParameterNames); err != nil {
		return err
	}

	// Validate parameter references within parameter bindings
	parameterBindings := map[string]struct{}{}
	for _, param := range tls.Task.Params {
		if _, ok := parameterBindings[param.Name]; ok {
			return apis.ErrGeneric("task parameter appears more than once", fmt.Sprintf("spec.task.params.%s", param.Name))
		}
		parameterBindings[param.Name] = struct{}{}
		if param.Value.Type == ParamTypeString {
			if err := substitution.ValidateVariable(fmt.Sprintf("param[%s]", param.Name), param.Value.StringVal, prefix, "task parameter", "spec.task", parameterNames); err != nil {
				return err
			}
			if err := substitution.ValidateVariableProhibited(fmt.Sprintf("param[%s]", param.Name), param.Value.StringVal, prefix, "task parameter", "spec.task", arrayParameterNames); err != nil {
				return err
			}
		} else {
			for _, arrayElement := range param.Value.ArrayVal {
				if err := substitution.ValidateVariable(fmt.Sprintf("param[%s]", param.Name), arrayElement, prefix, "task parameter", "spec.task", parameterNames); err != nil {
					return err
				}
				if err := substitution.ValidateVariableIsolated(fmt.Sprintf("param[%s]", param.Name), arrayElement, prefix, "task parameter", "spec.task", arrayParameterNames); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
