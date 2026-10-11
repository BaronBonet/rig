package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
// Claude lists for it.
//
// Claude runs in a private, empty directory and reads only the user's own
// settings, so no task workspace or shared directory can plant project
// settings for it. Every hook is disabled, so neither Rig's nor the user's
// SessionStart hooks fire for it, and no session is saved.
func (r *repository) ListModels(ctx context.Context) ([]core.ProviderModel, error) {
	ctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()

	workDir, err := os.MkdirTemp("", "rig-list-models-*")
	if err != nil {
		return nil, fmt.Errorf("list claude models: create working directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	session, err := r.runner.StartLineSession(ctx, subprocess.StartLineSessionOptions{
		Cwd:  workDir,
		Name: r.binary,
		Args: []string{
			"-p",
			"--input-format", "stream-json",
			"--output-format", "stream-json",
			"--verbose",
			"--no-session-persistence",
			"--setting-sources", "user",
			"--settings", `{"disableAllHooks":true}`,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	defer func() { _ = session.Close() }()

	models, err := initializeModels(session)
	if err != nil {
		return nil, fmt.Errorf("list claude models: %w", err)
	}

	return models, nil
}

type controlRequest struct {
	Type      string                `json:"type"`
	RequestID string                `json:"request_id"`
	Request   controlRequestPayload `json:"request"`
}

type controlRequestPayload struct {
	Subtype string `json:"subtype"`
}

// controlResponse is a control_response line. Other stream-json lines, such
// as hook progress, decode with an empty Type.
type controlResponse struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
		Response  struct {
			Models []initializeModel `json:"models"`
		} `json:"response"`
	} `json:"response"`
}

type initializeModel struct {
	// Value is what claude --model takes.
	Value                 string   `json:"value"`
	DisplayName           string   `json:"displayName"`
	SupportsEffort        bool     `json:"supportsEffort"`
	SupportedEffortLevels []string `json:"supportedEffortLevels"`
}

// initializeModels sends the initialize control request and reads lines until
// its response arrives.
func initializeModels(session subprocess.LineSession) ([]core.ProviderModel, error) {
	request, err := json.Marshal(controlRequest{
		Type:      "control_request",
		RequestID: listModelsRequestID,
		Request:   controlRequestPayload{Subtype: "initialize"},
	})
	if err != nil {
		return nil, fmt.Errorf("encode initialize request: %w", err)
	}
	if err := session.Send(string(request)); err != nil {
		return nil, fmt.Errorf("send initialize request: %w", err)
	}

	for {
		line, err := session.Receive()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("claude ended before answering initialize: %w", err)
		}
		if err != nil {
			return nil, fmt.Errorf("await initialize response: %w", err)
		}

		var message controlResponse
		if json.Unmarshal([]byte(line), &message) != nil ||
			message.Type != "control_response" ||
			message.Response.RequestID != listModelsRequestID {
			continue
		}
		if message.Response.Subtype != "success" {
			return nil, fmt.Errorf("initialize failed: %s", message.Response.Error)
		}

		var models []core.ProviderModel
		for _, model := range message.Response.Response.Models {
			if providerModel, ok := model.providerModel(); ok {
				models = append(models, providerModel)
			}
		}
		return models, nil
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
