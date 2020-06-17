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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"knative.dev/pkg/apis"
	duckv1beta1 "knative.dev/pkg/apis/duck/v1beta1"
)

const (
	TaskLoopRunKind = "TaskLoopRun"
)

var (
	taskLoopRunGroupVersionKind = schema.GroupVersionKind{
		Group:   SchemeGroupVersion.Group,
		Version: SchemeGroupVersion.Version,
		Kind:    TaskLoopRunKind,
	}
)

// +genclient
// +genreconciler
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskLoopRun represents a single execution of a TaskLoop.
// +k8s:openapi-gen=true
type TaskLoopRun struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec TaskLoopRunSpec `json:"spec,omitempty"`
	// +optional
	Status TaskLoopRunStatus `json:"status,omitempty"`
}

// TaskLoopRunSpec defines the desired state of TaskRun
type TaskLoopRunSpec struct {
	// +optional
	Params []Param `json:"params,omitempty"`
	// no more than one of the TaskLoopRef and TaskLoopSpec may be specified.
	// +optional
	TaskLoopRef *TaskLoopRef `json:"taskLoopRef,omitempty"`
	// +optional
	TaskLoopSpec *TaskLoopSpec `json:"taskLoopSpec,omitempty"`
	// +optional
	ServiceAccountName string `json:"serviceAccountName"`
	// Used for cancelling a TaskLoopRun
	// +optional
	Status TaskLoopRunSpecStatus `json:"status,omitempty"`
}

// TaskLoopRef can be used to refer to a specific instance of a TaskLoop.
type TaskLoopRef struct {
	// Name of the referent
	Name string `json:"name,omitempty"`
	// API version of the referent
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`
}

// TaskLoopRunSpecStatus defines the taskrun spec status the user can provide
type TaskLoopRunSpecStatus string

const (
	// TaskLoopRunSpecStatusCancelled indicates that the user wants to cancel the task
	TaskLoopRunSpecStatusCancelled = "TaskRunCancelled"
)

var taskLoopRunCondSet = apis.NewBatchConditionSet()

// TaskLoopRunStatus defines the observed state of TaskLoopRun
type TaskLoopRunStatus struct {
	duckv1beta1.Status `json:",inline"`
	// TaskLoopRunStatusFields inlines the status fields.
	TaskLoopRunStatusFields `json:",inline"`
}

// TaskLoopRunReason represents a reason for the TaskLoopRun "Succeeded" condition
type TaskLoopRunReason string

const (
	// TaskLoopRunReasonStarted is the reason set when the TaskLoopRun has just started
	TaskLoopRunReasonStarted TaskRunReason = "Started"

	// TaskLoopRunReasonRunning indicates that the TaskLoopRun is in progress
	TaskLoopRunReasonRunning TaskLoopRunReason = "Running"

	// TaskLoopRunReasonFailed indicates that one of the TaskRuns created from the TaskLoopRun failed
	TaskLoopRunReasonFailed TaskLoopRunReason = "Failed"

	// TaskLoopRunReasonSucceeded indicates that all of the TaskRuns created from the TaskLoopRun completed successfully
	TaskLoopRunReasonSucceeded TaskLoopRunReason = "Succeeded"

	// TaskLoopRunReasonCouldntGetTaskLoop indicates that the associated TaskLoop couldn't be retrieved
	TaskLoopRunReasonCouldntGetTaskLoop TaskLoopRunReason = "CouldntGetTaskLoop"

	// TaskLoopRunReasonFailedValidation indicates that the TaskLoop failed runtime validation
	TaskLoopRunReasonFailedValidation TaskLoopRunReason = "TaskLoopValidationFailed"
)

func (t TaskLoopRunReason) String() string {
	return string(t)
}

// TaskLoopRunStatusFields holds the fields of TaskLoopRun's status.  This is defined
// separately and inlined so that other types can readily consume these fields
// via duck typing.
type TaskLoopRunStatusFields struct {
	// StartTime is the time the run started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// CompletionTime is the time the run completed.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// map of TaskLoopTaskRunStatus with the taskRun name as the key
	// +optional
	TaskRuns map[string]*TaskLoopTaskRunStatus `json:"taskRuns,omitempty"`
	// TaskLoopSpec contains the exact spec used to instantiate the run
	TaskLoopSpec *TaskLoopSpec `json:"taskLoopSpec,omitempty"`
}

// TaskLoopTaskRunStatus contains the iteration number for this TaskRun and the TaskRun's Status
type TaskLoopTaskRunStatus struct {
	// iteration number
	Iteration int `json:"iteration,omitempty"`
	// Status is the TaskRunStatus for the corresponding TaskRun
	// +optional
	Status *TaskRunStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// TaskLoopRunList contains a list of TaskLoopRuns
type TaskLoopRunList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TaskLoopRun `json:"items"`
}

// GetOwnerReference gets the pipeline run as owner reference for any related objects
func (tlr *TaskLoopRun) GetOwnerReference() metav1.OwnerReference {
	return *metav1.NewControllerRef(tlr, taskLoopRunGroupVersionKind)
}

// IsStarted function check whether taskrun has valid start time set in its status
func (tlr *TaskLoopRun) IsStarted() bool {
	return tlr.Status.StartTime != nil && !tlr.Status.StartTime.IsZero()
}

// IsCancelled returns true if the TaskRun's spec status is set to Cancelled state
func (tlr *TaskLoopRun) IsCancelled() bool {
	return tlr.Spec.Status == TaskLoopRunSpecStatusCancelled
}

// IsDone returns true if the TaskLoopRun's status indicates that it is done.
func (tlr *TaskLoopRun) IsDone() bool {
	return !tlr.Status.GetCondition(apis.ConditionSucceeded).IsUnknown()
}

// InitializeConditions sets all conditions in taskRunCondSet to unknown for the TaskLoopRun
// and set the started time to the current time
func (tlrs *TaskLoopRunStatus) InitializeConditions() {
	if tlrs.TaskRuns == nil {
		tlrs.TaskRuns = make(map[string]*TaskLoopTaskRunStatus)
	}
	if tlrs.StartTime.IsZero() {
		tlrs.StartTime = &metav1.Time{Time: time.Now()}
	}
	conditionManager := taskLoopRunCondSet.Manage(tlrs)
	conditionManager.InitializeConditions()
	initialCondition := conditionManager.GetCondition(apis.ConditionSucceeded)
	initialCondition.Reason = TaskLoopRunReasonStarted.String()
	conditionManager.SetCondition(*initialCondition)
}

// SetCondition sets the condition, unsetting previous conditions with the same
// type as necessary.
func (tlrs *TaskLoopRunStatus) SetCondition(newCond *apis.Condition) {
	if newCond != nil {
		taskLoopRunCondSet.Manage(tlrs).SetCondition(*newCond)
	}
}

// MarkSucceeded changes the Succeeded condition to True with the provided reason and message.
func (tlrs *TaskLoopRunStatus) MarkSucceeded(reason, messageFormat string, messageA ...interface{}) {
	taskLoopRunCondSet.Manage(tlrs).MarkTrueWithReason(apis.ConditionSucceeded, reason, messageFormat, messageA...)
	succeeded := tlrs.GetCondition(apis.ConditionSucceeded)
	tlrs.CompletionTime = &succeeded.LastTransitionTime.Inner
}

// MarkFailed changes the Succeeded condition to False with the provided reason and message.
func (tlrs *TaskLoopRunStatus) MarkFailed(reason, messageFormat string, messageA ...interface{}) {
	taskLoopRunCondSet.Manage(tlrs).MarkFalse(apis.ConditionSucceeded, reason, messageFormat, messageA...)
	succeeded := tlrs.GetCondition(apis.ConditionSucceeded)
	tlrs.CompletionTime = &succeeded.LastTransitionTime.Inner
}

// MarkRunning changes the Succeeded condition to Unknown with the provided reason and message.
func (tlrs *TaskLoopRunStatus) MarkRunning(reason, messageFormat string, messageA ...interface{}) {
	taskLoopRunCondSet.Manage(tlrs).MarkUnknown(apis.ConditionSucceeded, reason, messageFormat, messageA...)
}
