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

	"github.com/tektoncd/pipeline/pkg/apis/pipeline"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	pipelineclient "github.com/tektoncd/pipeline/pkg/client/injection/client"
	taskloopinformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1beta1/taskloop"
	taskloopruninformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1beta1/tasklooprun"
	taskruninformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1beta1/taskrun"
	tasklooprunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1beta1/tasklooprun"
	"github.com/tektoncd/pipeline/pkg/reconciler/pipelinerun/config"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
)

// NewController instantiates a new controller.Impl from knative.dev/pkg/controller
func NewController(namespace string, images pipeline.Images) func(context.Context, configmap.Watcher) *controller.Impl {
	return func(ctx context.Context, cmw configmap.Watcher) *controller.Impl {

		logger := logging.FromContext(ctx)
		pipelineclientset := pipelineclient.Get(ctx)
		taskLoopInformer := taskloopinformer.Get(ctx)
		taskLoopRunInformer := taskloopruninformer.Get(ctx)
		taskRunInformer := taskruninformer.Get(ctx)

		c := &Reconciler{
			PipelineClientSet: pipelineclientset,
			taskLoopLister:    taskLoopInformer.Lister(),
			taskLoopRunLister: taskLoopRunInformer.Lister(),
			taskRunLister:     taskRunInformer.Lister(),
		}

		impl := tasklooprunreconciler.NewImpl(ctx, c, func(impl *controller.Impl) controller.Options {
			configStore := config.NewStore(images, logger.Named("config-store"))
			configStore.WatchConfigs(cmw)
			return controller.Options{
				AgentName:   v1beta1.TaskLoopRunKind,
				ConfigStore: configStore,
			}
		})

		logger.Info("Setting up event handlers")
		taskLoopRunInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    impl.Enqueue,
			UpdateFunc: controller.PassNew(impl.Enqueue),
			// TODO: Need DeleteFunc?  pipelinerun controller has it, taskrun controller does not
		})

		taskRunInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: controller.FilterGroupKind(v1beta1.Kind("TaskLoopRun")),
			Handler:    controller.HandleAll(impl.EnqueueControllerOf),
		})

		return impl
	}
}
