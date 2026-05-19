package taskdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

type socketUnaryCommand struct {
	handle func(context.Context, socketBackend, socketRequest) (socketEnvelope, error)
	name   string
}

var socketUnaryCommands = map[string]socketUnaryCommand{
	socketCommandHealth: {
		name:   socketCommandHealth,
		handle: handleHealthCommand,
	},
	socketCommandProtocolVersion: {
		name:   socketCommandProtocolVersion,
		handle: handleProtocolVersionCommand,
	},
	socketCommandFrontendBuildVersion: {
		name:   socketCommandFrontendBuildVersion,
		handle: handleFrontendBuildVersionCommand,
	},
	socketCommandDeleteTask: {
		name:   socketCommandDeleteTask,
		handle: handleDeleteTaskCommand,
	},
	socketCommandReconnectTaskSession: {
		name:   socketCommandReconnectTaskSession,
		handle: handleReconnectTaskSessionCommand,
	},
	socketCommandGetTaskActivity: {
		name:   socketCommandGetTaskActivity,
		handle: handleGetTaskActivityCommand,
	},
	socketCommandGetTaskTokenUsage: {
		name:   socketCommandGetTaskTokenUsage,
		handle: handleGetTaskTokenUsageCommand,
	},
	socketCommandLatestTaskStatus: {
		name:   socketCommandLatestTaskStatus,
		handle: handleLatestTaskStatusCommand,
	},
	socketCommandListRepoPullRequests: {
		name:   socketCommandListRepoPullRequests,
		handle: handleListRepoPullRequestsCommand,
	},
	socketCommandListTasks: {
		name:   socketCommandListTasks,
		handle: handleListTasksCommand,
	},
	socketCommandPullRequestStatus: {
		name:   socketCommandPullRequestStatus,
		handle: handlePullRequestStatusCommand,
	},
}

func writeSocketUnaryCommandResponse(
	ctx context.Context,
	encoder *json.Encoder,
	backend socketBackend,
	command socketUnaryCommand,
	req socketRequest,
) {
	if command.name == "" || command.handle == nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{
			Type:  socketEnvelopeError,
			Error: "socket unary command not configured",
		})
		return
	}

	resp, err := command.handle(ctx, backend, req)
	if err != nil {
		_ = writeSocketEnvelope(encoder, socketEnvelope{Type: socketEnvelopeError, Error: err.Error()})
		return
	}

	_ = writeSocketEnvelope(encoder, resp)
}

func handleHealthCommand(context.Context, socketBackend, socketRequest) (socketEnvelope, error) {
	return socketEnvelope{Type: socketEnvelopeHealth, OK: true}, nil
}

func handleProtocolVersionCommand(context.Context, socketBackend, socketRequest) (socketEnvelope, error) {
	return socketEnvelope{
		Type:            socketEnvelopeProtocolVersion,
		OK:              true,
		ProtocolVersion: currentFrontendProtocolVersion,
	}, nil
}

func handleFrontendBuildVersionCommand(context.Context, socketBackend, socketRequest) (socketEnvelope, error) {
	return socketEnvelope{
		Type:    socketEnvelopeFrontendBuildVersion,
		OK:      true,
		Version: currentFrontendBuildVersion,
	}, nil
}

func handleDeleteTaskCommand(ctx context.Context, backend socketBackend, req socketRequest) (socketEnvelope, error) {
	taskID, err := requiredSocketTaskID(req, socketCommandDeleteTask)
	if err != nil {
		return socketEnvelope{}, err
	}
	if err := backend.DeleteTask(ctx, taskID); err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{Type: socketEnvelopeTaskDeleted, OK: true}, nil
}

func handleReconnectTaskSessionCommand(
	ctx context.Context,
	backend socketBackend,
	req socketRequest,
) (socketEnvelope, error) {
	taskID, err := requiredSocketTaskID(req, socketCommandReconnectTaskSession)
	if err != nil {
		return socketEnvelope{}, err
	}
	if err := backend.ReconnectTaskSession(ctx, taskID); err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{Type: socketEnvelopeTaskSessionReconnect, OK: true}, nil
}

func handleGetTaskActivityCommand(
	ctx context.Context,
	backend socketBackend,
	req socketRequest,
) (socketEnvelope, error) {
	taskID, err := requiredSocketTaskID(req, socketCommandGetTaskActivity)
	if err != nil {
		return socketEnvelope{}, err
	}

	activity, err := backend.GetTaskActivity(ctx, taskID, req.Limit)
	if err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{Type: socketEnvelopeTaskActivity, OK: true, Activity: activity}, nil
}

func handleGetTaskTokenUsageCommand(
	ctx context.Context,
	backend socketBackend,
	req socketRequest,
) (socketEnvelope, error) {
	taskID, err := requiredSocketTaskID(req, socketCommandGetTaskTokenUsage)
	if err != nil {
		return socketEnvelope{}, err
	}

	usage, err := backend.GetTaskTokenUsage(ctx, taskID)
	if err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{Type: socketEnvelopeTaskTokenUsage, OK: true, Usage: usage}, nil
}

func handleLatestTaskStatusCommand(
	ctx context.Context,
	backend socketBackend,
	req socketRequest,
) (socketEnvelope, error) {
	update, err := backend.LatestTaskStatus(ctx, strings.TrimSpace(req.TaskID))
	if err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{Type: socketEnvelopeTaskStatusSnapshot, OK: true, Update: update}, nil
}

func handleListRepoPullRequestsCommand(
	ctx context.Context,
	backend socketBackend,
	req socketRequest,
) (socketEnvelope, error) {
	prs, err := backend.ListRepoPullRequests(ctx, strings.TrimSpace(req.Cwd))
	if err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{
		Type:         socketEnvelopeRepoPullRequestsList,
		OK:           true,
		PullRequests: prs,
	}, nil
}

func handleListTasksCommand(ctx context.Context, backend socketBackend, _ socketRequest) (socketEnvelope, error) {
	tasks, err := backend.ListTasks(ctx)
	if err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{Type: socketEnvelopeTasksList, OK: true, Tasks: tasks}, nil
}

func handlePullRequestStatusCommand(
	ctx context.Context,
	backend socketBackend,
	req socketRequest,
) (socketEnvelope, error) {
	status, err := backend.PullRequestStatus(
		ctx,
		strings.TrimSpace(req.Cwd),
		strings.TrimSpace(req.BranchName),
	)
	if err != nil {
		return socketEnvelope{}, err
	}

	if status == nil {
		status = &core.PRStatus{State: core.PRStateNone}
	}
	return socketEnvelope{
		Type: socketEnvelopePullRequestStatus,
		OK:   true,
		PR:   status,
	}, nil
}

func requiredSocketTaskID(req socketRequest, command string) (string, error) {
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		return "", errors.New(command + " task_id required")
	}
	return taskID, nil
}
