package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

const (
	// listModelsTimeout bounds one model listing, from starting Claude to its
	// initialize response.
	listModelsTimeout = 30 * time.Second
	// listModelsRequestID identifies the initialize control request whose
	// response carries the model list.
	listModelsRequestID = "rig-list-models"
	// defaultModelValue is Claude's entry for the account's default model. Rig
	// leaves it out: it expresses the default by passing no model flag.
	defaultModelValue = "default"
)

// ListModels asks Claude for the models it offers with the initialize control
// request of its stream-json protocol, the request behind the Claude Agent
// SDK's supportedModels. A model's reasoning levels are the effort levels
// Claude lists for it. Claude runs isolated, as startControlSession starts it.
func (r *repository) ListModels(ctx context.Context) ([]core.ProviderModel, error) {
	ctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()

	session, err := r.startControlSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("list claude models: %w", err)
	}
	defer func() { _ = session.Close() }()

	models, err := initializeModels(session)
	if err != nil {
		return nil, fmt.Errorf("list claude models: %w", err)
	}

	return models, nil
}

// startControlSession starts Claude to answer control requests of its
// stream-json protocol, with extraArgs after the flags that isolate it. The
// caller must Close the session.
//
// Claude runs in Rig's control directory and reads only the user's own
// settings, so no task workspace or shared directory can plant project
// settings for it. Every hook is disabled, so neither Rig's nor the user's
// SessionStart hooks fire for it, and no session is saved. Claude keeps a
// project directory under ~/.claude/projects for every directory it starts
// in, so every control session shares the one directory, which stays.
func (r *repository) startControlSession(
	ctx context.Context,
	extraArgs ...string,
) (subprocess.LineSession, error) {
	workDir, err := r.controlDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return nil, fmt.Errorf("create claude control directory: %w", err)
	}

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--no-session-persistence",
		"--setting-sources", "user",
		"--settings", `{"disableAllHooks":true}`,
	}
	session, err := r.runner.StartLineSession(ctx, subprocess.StartLineSessionOptions{
		Cwd:  workDir,
		Name: r.binary,
		Args: append(args, extraArgs...),
	})
	if err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}

	return session, nil
}

// controlDir is the directory Claude runs in to answer Rig's control
// requests: an empty one of Rig's own, kept between sessions.
func (r *repository) controlDir() (string, error) {
	dataDir, err := r.resolveRigDataDir()
	if err != nil {
		return "", err
	}
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return "", fmt.Errorf("rig data dir is required")
	}

	return filepath.Join(dataDir, "claude", "control"), nil
}

type controlRequest struct {
	Type      string                `json:"type"`
	RequestID string                `json:"request_id"`
	Request   controlRequestPayload `json:"request"`
}

type controlRequestPayload struct {
	Subtype string `json:"subtype"`
	// Detail is the level of detail a get_context_usage request asks for.
	Detail string `json:"detail,omitempty"`
}

// controlResponse is a control_response line. Other stream-json lines, such
// as hook progress, decode with an empty Type.
type controlResponse struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Error     string          `json:"error"`
		Response  json.RawMessage `json:"response"`
	} `json:"response"`
}

// initializeResponse is the payload of a successful initialize response.
type initializeResponse struct {
	Models []initializeModel `json:"models"`
}

type initializeModel struct {
	// Value is what claude --model takes.
	Value                 string   `json:"value"`
	DisplayName           string   `json:"displayName"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// initializeModels sends the initialize control request and returns the
// models its response lists.
func initializeModels(session subprocess.LineSession) ([]core.ProviderModel, error) {
	var response initializeResponse
	if err := exchangeControlRequest(
		session,
		listModelsRequestID,
		controlRequestPayload{Subtype: "initialize"},
		&response,
	); err != nil {
		return nil, err
	}

	var models []core.ProviderModel
	for _, model := range response.Models {
		if providerModel, ok := model.providerModel(); ok {
			models = append(models, providerModel)
		}
	}
	return models, nil
}

// exchangeControlRequest sends a control request and reads lines until its
// response arrives, decoding a successful response's payload into response,
// unless response is nil. Lines that are not that response, such as hook
// progress or the responses to other requests, are skipped. An error response
// is an error.
func exchangeControlRequest(
	session subprocess.LineSession,
	requestID string,
	payload controlRequestPayload,
	response any,
) error {
	request, err := json.Marshal(controlRequest{
		Type:      "control_request",
		RequestID: requestID,
		Request:   payload,
	})
	if err != nil {
		return fmt.Errorf("encode %s request: %w", payload.Subtype, err)
	}
	if err := session.Send(string(request)); err != nil {
		return fmt.Errorf("send %s request: %w", payload.Subtype, err)
	}

	for {
		line, err := session.Receive()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("claude ended before answering %s: %w", payload.Subtype, err)
		}
		if err != nil {
			return fmt.Errorf("await %s response: %w", payload.Subtype, err)
		}

		var message controlResponse
		if json.Unmarshal([]byte(line), &message) != nil ||
			message.Type != "control_response" ||
			message.Response.RequestID != requestID {
			continue
		}
		if message.Response.Subtype != "success" {
			return fmt.Errorf("%s failed: %s", payload.Subtype, message.Response.Error)
		}
		if response == nil || len(message.Response.Response) == 0 {
			return nil
		}
		if err := json.Unmarshal(message.Response.Response, response); err != nil {
			return fmt.Errorf("decode %s response: %w", payload.Subtype, err)
		}
		return nil
	}
}

func (m initializeModel) providerModel() (core.ProviderModel, bool) {
	id := strings.TrimSpace(m.Value)
	if id == "" || id == defaultModelValue {
		return core.ProviderModel{}, false
	}

	displayName := strings.TrimSpace(m.DisplayName)
	if displayName == "" {
		displayName = id
	}

	var levels []string
	if m.SupportsEffort {
		for _, level := range m.SupportedEffortLevels {
			if level = strings.TrimSpace(level); level != "" {
				levels = append(levels, level)
			}
		}
	}

	return core.ProviderModel{ID: id, DisplayName: displayName, ReasoningLevels: levels}, true
}
