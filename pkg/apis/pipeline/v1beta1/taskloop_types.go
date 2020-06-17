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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskLoop iteratively executes a Task over elements in an array.
// +k8s:openapi-gen=true
type TaskLoop struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata"`

	// Spec holds the desired state of the TaskLoop from the client
	// +optional
	Spec TaskLoopSpec `json:"spec"`
}

// TaskLoopSpec defines the desired state of the TaskLoop
type TaskLoopSpec struct {
	// Params declares a list of input parameters that must be supplied when
	// this TaskLoop is run (same parameters as the Task).
	Params []ParamSpec `json:"params,omitempty"`

	// WithItems is the array of elements used to iterate the Task.
	WithItems []string `json:"withItems,omitempty"`

	// Task is the task to run
	Task TaskLoopTask `json:"task"`
}

// TaskLoopTask defines the task to run.
type TaskLoopTask struct {
	// TaskRef is a reference to a task definition.
	// +optional
	TaskRef *TaskRef `json:"taskRef,omitempty"`

	// TaskSpec is a specification of a task
	// +optional
	TaskSpec *TaskSpec `json:"taskSpec,omitempty"`

	// Parameters declares parameters passed to this task.
	// +optional
	Params []Param `json:"params,omitempty"`

	// Time after which the TaskRun times out.
	// +optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskLoopList contains a list of TaskLoops
type TaskLoopList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskLoop `json:"items"`
}
