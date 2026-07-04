// Package taskdaemon's socket protocol is defined in this file: every unary
// operation is declared exactly once as a unaryOp descriptor, used by both
// the frontend client (callUnary) and the daemon dispatcher (serveUnary), so
// the two sides of the seam cannot drift. The two stream shapes follow, each
// with its client decode and server encode halves co-located.
//
// The wire is version-locked: TUI and daemon are always the same binary, so
// changing request or response shapes only requires bumping
// currentFrontendProtocolVersion. Only the handshake probes in protocol.go
// have a frozen shape. See docs/adr/0002-version-locked-socket-protocol.md.
package taskdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/BaronBonet/rig/internal/core"
)

type unaryOp[Req, Resp any] struct {
	command  string
	envelope string
	call     func(ctx context.Context, svc core.TaskService, req Req) (Resp, error)
}

// emptyResponse is the payload of operations that only acknowledge success.
type emptyResponse struct{}

type taskIDRequest struct {
	TaskID string `json:"task_id"`
}

type taskActivityRequest struct {
	TaskID string `json:"task_id"`
	Limit  int    `json:"limit,omitempty"`
}

type repoPullRequestsRequest struct {
	Cwd string `json:"cwd"`
}

type pullRequestStatusRequest struct {
	Cwd        string `json:"cwd"`
	BranchName string `json:"branch_name"`
}

type saveProviderSetupRequest struct {
	ProviderSetup *core.ProviderSetup `json:"provider_setup"`
}

type switchTaskProviderRequest struct {
	TaskID   string        `json:"task_id"`
	Provider core.Provider `json:"provider"`
}

func requiredTaskID(command string, taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", errors.New(command + " task_id required")
	}
	return taskID, nil
}

var opGetTaskActivity = unaryOp[taskActivityRequest, []core.TaskActivityEvent]{
	command:  "get_task_activity",
	envelope: "task_activity",
	call: func(ctx context.Context, svc core.TaskService, req taskActivityRequest) ([]core.TaskActivityEvent, error) {
		taskID, err := requiredTaskID("get_task_activity", req.TaskID)
		if err != nil {
			return nil, err
		}
		return svc.GetTaskActivity(ctx, taskID, req.Limit)
	},
}

var opGetTaskTokenUsage = unaryOp[taskIDRequest, *core.TaskTokenUsage]{
	command:  "get_task_token_usage",
	envelope: "task_token_usage",
	call: func(ctx context.Context, svc core.TaskService, req taskIDRequest) (*core.TaskTokenUsage, error) {
		taskID, err := requiredTaskID("get_task_token_usage", req.TaskID)
		if err != nil {
			return nil, err
		}
		return svc.GetTaskTokenUsage(ctx, taskID)
	},
}

var opListRepoPullRequests = unaryOp[repoPullRequestsRequest, []core.RepoPullRequest]{
	command:  "list_repo_pull_requests",
	envelope: "repo_pull_requests_list",
	call: func(ctx context.Context, svc core.TaskService, req repoPullRequestsRequest) ([]core.RepoPullRequest, error) {
		return svc.ListRepoPullRequests(ctx, strings.TrimSpace(req.Cwd))
	},
}

var opPullRequestStatus = unaryOp[pullRequestStatusRequest, *core.PRStatus]{
	command:  "pull_request_status",
	envelope: "pull_request_status",
	call: func(ctx context.Context, svc core.TaskService, req pullRequestStatusRequest) (*core.PRStatus, error) {
		status, err := svc.PullRequestStatus(ctx, strings.TrimSpace(req.Cwd), strings.TrimSpace(req.BranchName))
		if err != nil {
			return nil, err
		}
		if status == nil {
			status = &core.PRStatus{State: core.PRStateNone}
		}
		return status, nil
	},
}

var opReconnectTaskSession = unaryOp[taskIDRequest, emptyResponse]{
	command:  "reconnect_task_session",
	envelope: "task_session_reconnected",
	call: func(ctx context.Context, svc core.TaskService, req taskIDRequest) (emptyResponse, error) {
		taskID, err := requiredTaskID("reconnect_task_session", req.TaskID)
		if err != nil {
			return emptyResponse{}, err
		}
		return emptyResponse{}, svc.ReconnectTaskSession(ctx, taskID)
	},
}

var opGetProviderSetup = unaryOp[emptyResponse, *core.ProviderSetup]{
	command:  "get_provider_setup",
	envelope: "provider_setup",
	call: func(ctx context.Context, svc core.TaskService, _ emptyResponse) (*core.ProviderSetup, error) {
		return svc.GetProviderSetup(ctx)
	},
}

var opSaveProviderSetup = unaryOp[saveProviderSetupRequest, emptyResponse]{
	command:  "save_provider_setup",
	envelope: "provider_setup_saved",
	call: func(ctx context.Context, svc core.TaskService, req saveProviderSetupRequest) (emptyResponse, error) {
		if req.ProviderSetup == nil {
			return emptyResponse{}, errors.New("save_provider_setup provider_setup required")
		}
		return emptyResponse{}, svc.SaveProviderSetup(ctx, *req.ProviderSetup)
	},
}

var opDetectProviders = unaryOp[emptyResponse, []core.ProviderDetection]{
	command:  "detect_providers",
	envelope: "provider_detections",
	call: func(ctx context.Context, svc core.TaskService, _ emptyResponse) ([]core.ProviderDetection, error) {
		return svc.DetectProviders(ctx)
	},
}

var opSwitchTaskProvider = unaryOp[switchTaskProviderRequest, *core.Task]{
	command:  "switch_task_provider",
	envelope: "task_provider_switched",
	call: func(ctx context.Context, svc core.TaskService, req switchTaskProviderRequest) (*core.Task, error) {
		taskID, err := requiredTaskID("switch_task_provider", req.TaskID)
		if err != nil {
			return nil, err
		}
		provider := core.Provider(strings.TrimSpace(string(req.Provider)))
		if provider == "" {
			return nil, errors.New("switch_task_provider provider required")
		}
		return svc.SwitchTaskProvider(ctx, taskID, provider)
	},
}

var opDeleteTask = unaryOp[taskIDRequest, emptyResponse]{
	command:  "delete_task",
	envelope: "task_deleted",
	call: func(ctx context.Context, svc core.TaskService, req taskIDRequest) (emptyResponse, error) {
		taskID, err := requiredTaskID("delete_task", req.TaskID)
		if err != nil {
			return emptyResponse{}, err
		}
		return emptyResponse{}, svc.DeleteTask(ctx, taskID)
	},
}

var opListTasks = unaryOp[emptyResponse, []*core.Task]{
	command:  "list_tasks",
	envelope: "tasks_list",
	call: func(ctx context.Context, svc core.TaskService, _ emptyResponse) ([]*core.Task, error) {
		return svc.ListTasks(ctx)
	},
}

var opLatestTaskStatus = unaryOp[taskIDRequest, *core.TaskStatusUpdate]{
	command:  "latest_task_status",
	envelope: "task_status_snapshot",
	call: func(ctx context.Context, svc core.TaskService, req taskIDRequest) (*core.TaskStatusUpdate, error) {
		return svc.LatestTaskStatus(ctx, strings.TrimSpace(req.TaskID))
	},
}

// unaryHandler is the uniform server-side shape every descriptor is adapted
// into for dispatch.
type unaryHandler func(ctx context.Context, svc core.TaskService, payload json.RawMessage) socketEnvelope

func serveUnary[Req, Resp any](op unaryOp[Req, Resp]) unaryHandler {
	return func(ctx context.Context, svc core.TaskService, payload json.RawMessage) socketEnvelope {
		var req Req
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &req); err != nil {
				return errorEnvelope(fmt.Errorf("%s: decode request: %w", op.command, err))
			}
		}

		resp, err := op.call(ctx, svc, req)
		if err != nil {
			return errorEnvelope(err)
		}

		body, err := json.Marshal(resp)
		if err != nil {
			return errorEnvelope(fmt.Errorf("%s: encode response: %w", op.command, err))
		}

		return socketEnvelope{Type: op.envelope, OK: true, Payload: body}
	}
}

// socketUnaryHandlers is the daemon dispatch table. Descriptors register on
// both sides: here for serving, and in the frontend client methods via
// callUnary. The frontend cannot compile without a descriptor per TaskService
// method, which keeps this table honest.
var socketUnaryHandlers = map[string]unaryHandler{
	opGetTaskActivity.command:      serveUnary(opGetTaskActivity),
	opGetTaskTokenUsage.command:    serveUnary(opGetTaskTokenUsage),
	opListRepoPullRequests.command: serveUnary(opListRepoPullRequests),
	opPullRequestStatus.command:    serveUnary(opPullRequestStatus),
	opReconnectTaskSession.command: serveUnary(opReconnectTaskSession),
	opGetProviderSetup.command:     serveUnary(opGetProviderSetup),
	opSaveProviderSetup.command:    serveUnary(opSaveProviderSetup),
	opDetectProviders.command:      serveUnary(opDetectProviders),
	opSwitchTaskProvider.command:   serveUnary(opSwitchTaskProvider),
	opDeleteTask.command:           serveUnary(opDeleteTask),
	opListTasks.command:            serveUnary(opListTasks),
	opLatestTaskStatus.command:     serveUnary(opLatestTaskStatus),
}

func errorEnvelope(err error) socketEnvelope {
	return socketEnvelope{Type: socketEnvelopeError, Error: err.Error()}
}

// callUnary drives one descriptor from the client side: encode the request,
// send it over the socket, check the envelope type, decode the response.
func callUnary[Req, Resp any](
	ctx context.Context,
	f *frontend,
	op unaryOp[Req, Resp],
	req Req,
) (Resp, error) {
	var zero Resp

	payload, err := json.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("%s: encode request: %w", op.command, err)
	}

	env, err := f.send(ctx, socketRequest{Command: op.command, Payload: payload})
	if err != nil {
		return zero, err
	}
	if env.Type != op.envelope || !env.OK {
		return zero, unexpectedResponseError(op.command, *env)
	}

	var resp Resp
	if len(env.Payload) > 0 {
		if err := json.Unmarshal(env.Payload, &resp); err != nil {
			return zero, fmt.Errorf("%s: decode response: %w", op.command, err)
		}
	}

	return resp, nil
}

func unexpectedResponseError(command string, resp socketEnvelope) error {
	if resp.Error != "" {
		return errors.New(resp.Error)
	}

	return fmt.Errorf("unexpected %s response %q", command, resp.Type)
}

// ---------------------------------------------------------------------------
// Task-create event stream, shared by create_task and retry_task_creation.
//
// Wire shape: zero or more task_create_progress envelopes, then exactly one
// terminal envelope — task_created on success, error (with an optional task
// payload) on failure. encodeTaskCreateEvent and decodeTaskCreateEvent are
// the two halves of the contract; change them together.
// ---------------------------------------------------------------------------

const (
	socketCommandCreateTask        = "create_task"
	socketCommandRetryTaskCreation = "retry_task_creation"

	socketEnvelopeTaskCreateProgress = "task_create_progress"
	socketEnvelopeTaskCreated        = "task_created"
)

// taskCreateErrorPayload carries the optional partially-created task attached
// to a terminal create error, so the frontend can offer retry.
type taskCreateErrorPayload struct {
	Task *core.Task `json:"task,omitempty"`
}

func encodeTaskCreateEvent(event core.TaskCreateEvent) (socketEnvelope, error) {
	switch {
	case event.Err != nil:
		payload, err := json.Marshal(taskCreateErrorPayload{Task: event.Task})
		if err != nil {
			return socketEnvelope{}, err
		}
		return socketEnvelope{Type: socketEnvelopeError, Error: event.Err.Error(), Payload: payload}, nil
	case event.Task != nil:
		payload, err := json.Marshal(event.Task)
		if err != nil {
			return socketEnvelope{}, err
		}
		return socketEnvelope{Type: socketEnvelopeTaskCreated, OK: true, Payload: payload}, nil
	case event.Progress != nil:
		payload, err := json.Marshal(event.Progress)
		if err != nil {
			return socketEnvelope{}, err
		}
		return socketEnvelope{Type: socketEnvelopeTaskCreateProgress, OK: true, Payload: payload}, nil
	default:
		return socketEnvelope{}, fmt.Errorf("task create event has no progress, task, or error")
	}
}

func decodeTaskCreateEvent(env socketEnvelope) (core.TaskCreateEvent, error) {
	switch env.Type {
	case socketEnvelopeTaskCreateProgress:
		var progress core.TaskCreateProgressEvent
		if err := json.Unmarshal(env.Payload, &progress); err != nil {
			return core.TaskCreateEvent{}, err
		}
		return core.TaskCreateEvent{Progress: &progress}, nil
	case socketEnvelopeTaskCreated:
		var task core.Task
		if err := json.Unmarshal(env.Payload, &task); err != nil {
			return core.TaskCreateEvent{}, err
		}
		return core.TaskCreateEvent{Task: &task}, nil
	case socketEnvelopeError:
		if env.Error == "" {
			return core.TaskCreateEvent{}, fmt.Errorf("task create error envelope without message")
		}
		var payload taskCreateErrorPayload
		if len(env.Payload) > 0 {
			if err := json.Unmarshal(env.Payload, &payload); err != nil {
				return core.TaskCreateEvent{}, err
			}
		}
		return core.TaskCreateEvent{Err: errors.New(env.Error), Task: payload.Task}, nil
	default:
		return core.TaskCreateEvent{}, fmt.Errorf("unexpected task create stream message %q", env.Type)
	}
}

func receiveTaskCreateEvent(decoder *json.Decoder) (core.TaskCreateEvent, error) {
	var msg socketEnvelope
	if err := decoder.Decode(&msg); err != nil {
		if errors.Is(err, io.EOF) {
			return core.TaskCreateEvent{}, err
		}
		return core.TaskCreateEvent{}, err
	}

	return decodeTaskCreateEvent(msg)
}

// ---------------------------------------------------------------------------
// Task-status subscription stream.
//
// Wire shape: one subscribed ack, then task_status_update envelopes until the
// client disconnects or the subscription ends. encodeTaskStatusUpdate and
// receiveTaskStatusUpdate are the two halves of the contract; change them
// together.
// ---------------------------------------------------------------------------

const (
	socketCommandSubscribeTaskStatus = "subscribe_task_status"

	socketEnvelopeSubscribed       = "subscribed"
	socketEnvelopeTaskStatusUpdate = "task_status_update"
)

func encodeTaskStatusUpdate(update core.TaskStatusUpdate) (socketEnvelope, error) {
	payload, err := json.Marshal(update)
	if err != nil {
		return socketEnvelope{}, err
	}

	return socketEnvelope{Type: socketEnvelopeTaskStatusUpdate, OK: true, Payload: payload}, nil
}

func receiveTaskStatusUpdate(decoder *json.Decoder) (*core.TaskStatusUpdate, error) {
	var msg socketEnvelope
	if err := decoder.Decode(&msg); err != nil {
		return nil, err
	}
	if msg.Type == socketEnvelopeError && msg.Error != "" {
		return nil, errors.New(msg.Error)
	}
	if msg.Type != socketEnvelopeTaskStatusUpdate || len(msg.Payload) == 0 {
		return nil, fmt.Errorf("unexpected %s stream message %q", socketCommandSubscribeTaskStatus, msg.Type)
	}

	var update core.TaskStatusUpdate
	if err := json.Unmarshal(msg.Payload, &update); err != nil {
		return nil, err
	}

	return &update, nil
}
