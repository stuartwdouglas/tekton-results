package taskrun

import (
	"context"
	"fmt"

	"github.com/tektoncd/results/pkg/apis/config"
	"github.com/tektoncd/results/pkg/taskrunmetrics"
	"github.com/tektoncd/results/pkg/watcher/results"

	pipelinev1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"knative.dev/pkg/controller"
	knativereconciler "knative.dev/pkg/reconciler"

	"github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1beta1"
	"github.com/tektoncd/results/pkg/watcher/reconciler"
	"github.com/tektoncd/results/pkg/watcher/reconciler/dynamic"
	pb "github.com/tektoncd/results/proto/v1alpha2/results_go_proto"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"knative.dev/pkg/logging"
)

// Reconciler represents taskRun watcher logic
type Reconciler struct {
	// Inline LeaderAwareFuncs to support leader election.
	knativereconciler.LeaderAwareFuncs

	resultsClient  pb.ResultsClient
	logsClient     pb.LogsClient
	lister         v1beta1.TaskRunLister
	pipelineClient versioned.Interface
	cfg            *reconciler.Config
	metrics        *taskrunmetrics.Recorder
	configStore    *config.Store
}

// Check that our Reconciler is LeaderAware.
var _ knativereconciler.LeaderAware = (*Reconciler)(nil)

// Reconcile makes new watcher reconcile cycle to handle TaskRun.
func (r *Reconciler) Reconcile(ctx context.Context, key string) error {
	logger := logging.FromContext(ctx).With(zap.String("results.tekton.dev/kind", "TaskRun"))

	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		logger.Errorf("invalid resource key: %s", key)
		return nil
	}

	if !r.IsLeaderFor(types.NamespacedName{Namespace: namespace, Name: name}) {
		logger.Debug("Skipping TaskRun key because this instance isn't its leader")
		return controller.NewSkipKey(key)
	}

	logger.Info("Reconciling TaskRun")

	tr, err := r.lister.TaskRuns(namespace).Get(name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Debug("Skipping key: object is no longer available")
			return controller.NewSkipKey(key)
		}
		return fmt.Errorf("error reading TaskRun from the indexer: %w", err)
	}

	taskRunClient := &dynamic.TaskRunClient{
		TaskRunInterface: r.pipelineClient.TektonV1beta1().TaskRuns(namespace),
	}

	dyn := dynamic.NewDynamicReconciler(r.resultsClient, r.logsClient, taskRunClient, r.cfg)
	dyn.AfterDeletion = func(ctx context.Context, o results.Object) error {
		tr := o.(*pipelinev1beta1.TaskRun)
		return r.metrics.DurationAndCountDeleted(ctx, r.configStore.Load().Metrics, tr)
	}
	return dyn.Reconcile(logging.WithLogger(ctx, logger), tr)
}
