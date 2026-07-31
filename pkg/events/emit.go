package events

import (
	"context"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func NewEventEmitter(client kubernetes.Interface, logger *zap.SugaredLogger) *EventEmitter {
	return &EventEmitter{
		client: client,
		logger: logger,
	}
}

type EventEmitter struct {
	client kubernetes.Interface
	logger *zap.SugaredLogger
}

func (e *EventEmitter) SetLogger(logger *zap.SugaredLogger) {
	e.logger = logger
}

func (e *EventEmitter) EmitMessage(repo *v1alpha1.Repository, loggerLevel zapcore.Level, reason, message string) {
	if repo != nil && e.client != nil {
		event := makeEvent(repo, loggerLevel, reason, message)
		if _, err := e.client.CoreV1().Events(event.Namespace).Create(context.Background(), event, metav1.CreateOptions{}); err != nil {
			if e.logger != nil {
				e.logger.Infof("Cannot create event: %s", err.Error())
			}
		}
	}

	if e.logger != nil {
		//nolint
		switch loggerLevel {
		case zapcore.DebugLevel:
			e.logger.Debug(message)
		case zapcore.ErrorLevel:
			e.logger.Error(message)
		case zapcore.InfoLevel:
			e.logger.Info(message)
		case zapcore.WarnLevel:
			e.logger.Warn(message)
		}
	}
}

const componentName = "Pipelines As Code"

func makeEvent(repo *v1alpha1.Repository, loggerLevel zapcore.Level, reason, message string) *v1.Event {
	now := metav1.Now()
	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: repo.Name + "-",
			Namespace:    repo.Namespace,
			Labels: map[string]string{
				keys.Repository: formatting.CleanValueKubernetes(repo.Name),
			},
			Annotations: map[string]string{
				keys.Repository: repo.Name,
			},
		},
		Message: message,
		Reason:  reason,
		Type:    v1.EventTypeWarning,
		InvolvedObject: v1.ObjectReference{
			APIVersion:      pipelinesascode.V1alpha1Version,
			Kind:            pipelinesascode.RepositoryKind,
			Namespace:       repo.Namespace,
			Name:            repo.Name,
			UID:             repo.UID,
			ResourceVersion: repo.ResourceVersion,
		},
		Source: v1.EventSource{
			Component: componentName,
		},
		ReportingController: componentName,
		FirstTimestamp:      now,
		LastTimestamp:       now,
		Count:               1,
	}
	if loggerLevel == zap.InfoLevel {
		event.Type = v1.EventTypeNormal
	}
	return event
}
