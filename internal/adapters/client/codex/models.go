package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/BaronBonet/rig/internal/core"
	"github.com/BaronBonet/rig/internal/pkg/subprocess"
)

const (
	// listModelsTimeout bounds one model listing, from starting the
	// app-server to its last model/list page.
	listModelsTimeout = 30 * time.Second
	// appServerClientName and appServerClientVersion identify Rig to the
	// app-server, which reports them in Codex's user agent.
	appServerClientName    = "rig"
	appServerClientVersion = "dev"
	// rpcMethodNotFound is the JSON-RPC error code for a method the receiver
	// does not offer.
	rpcMethodNotFound = -32601
)

// ListModels asks codex app-server for the models the user's Codex offers,
// over its JSON-RPC protocol on stdio. A model's reasoning levels are the
// reasoning efforts Codex lists for it. Hidden models are left out.
//
// The app-server stops answering once its stdin closes, so the exchange runs
// line by line on a session whose stdin stays open until it ends. It runs in
// a private, empty directory, so no project config changes the list.
func (r *repository) ListModels(ctx context.Context) ([]core.ProviderModel, error) {
	ctx, cancel := context.WithTimeout(ctx, listModelsTimeout)
	defer cancel()

	codexHome, err := r.taskCodexHome()
	if err != nil {
		return nil, fmt.Errorf("list codex models: %w", err)
	}
	command := r.codexTaskCommand(codexHome, "app-server")

	workDir, err := os.MkdirTemp("", "rig-list-models-*")
	if err != nil {
		return nil, fmt.Errorf("list codex models: create working directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	session, err := r.runner.StartLineSession(ctx, subprocess.StartLineSessionOptions{
		Cwd:  workDir,
		Name: command[0],
		Args: command[1:],
	})
	if err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	defer func() { _ = session.Close() }()

	client := &appServerClient{session: session}
	models, err := client.listModels()
	if err != nil {
		return nil, fmt.Errorf("list codex models: %w", err)
	}

	return models, nil
}

// appServerClient speaks codex app-server's JSON-RPC protocol over a line
// session, one JSON message per line.
type appServerClient struct {
	session subprocess.LineSession
	lastID  int
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
}

type rpcErrorResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   rpcError        `json:"error"`
}

// rpcMessage is any message the app-server writes. A response carries the id
// of its request and no method; notifications and requests from the server
// carry a method.
type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type initializeParams struct {
	ClientInfo clientInfo `json:"clientInfo"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type modelListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// modelListPage is one page of model/list. A null or empty nextCursor ends
// the list.
type modelListPage struct {
	Data       []appServerModel `json:"data"`
	NextCursor string           `json:"nextCursor"`
}

type appServerModel struct {
	// Model is the slug codex -m takes.
	Model                     string `json:"model"`
	DisplayName               string `json:"displayName"`
	Hidden                    bool   `json:"hidden"`
	SupportedReasoningEfforts []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	} `json:"supportedReasoningEfforts"`
}

func (c *appServerClient) listModels() ([]core.ProviderModel, error) {
	initialize := initializeParams{ClientInfo: clientInfo{Name: appServerClientName, Version: appServerClientVersion}}
	if err := c.call("initialize", initialize, nil); err != nil {
		return nil, err
	}
	if err := c.send(rpcNotification{JSONRPC: "2.0", Method: "initialized"}); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	var models []core.ProviderModel
	params := modelListParams{}
	sentCursors := map[string]bool{}
	for {
		var page modelListPage
		if err := c.call("model/list", params, &page); err != nil {
			return nil, err
		}
		for _, model := range page.Data {
			if providerModel, ok := model.providerModel(); ok {
				models = append(models, providerModel)
			}
		}
		if page.NextCursor == "" {
			return models, nil
		}
		if sentCursors[page.NextCursor] {
			return nil, fmt.Errorf("model/list returned cursor %q again", page.NextCursor)
		}
		sentCursors[page.NextCursor] = true
		params.Cursor = page.NextCursor
	}
}

// call sends one request and decodes the result of its response into result.
// Until the response arrives it skips notifications and other lines, and
// turns down requests from the server, so a server waiting on one of its own
// requests never stalls the exchange.
func (c *appServerClient) call(method string, params any, result any) error {
	c.lastID++
	id := c.lastID
	if err := c.send(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return fmt.Errorf("send %s: %w", method, err)
	}

	for {
		line, err := c.session.Receive()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("app-server ended before answering %s: %w", method, err)
		}
		if err != nil {
			return fmt.Errorf("await %s response: %w", method, err)
		}

		var message rpcMessage
		if json.Unmarshal([]byte(line), &message) != nil {
			continue
		}
		if message.isServerRequest() {
			if err := c.send(rpcErrorResponse{
				JSONRPC: "2.0",
				ID:      message.ID,
				Error:   rpcError{Code: rpcMethodNotFound, Message: "method not found"},
			}); err != nil {
				return fmt.Errorf("turn down %s request: %w", message.Method, err)
			}
			continue
		}
		if !message.answers(id) {
			continue
		}
		if message.Error != nil {
			return fmt.Errorf("%s failed: %s (code %d)", method, message.Error.Message, message.Error.Code)
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(message.Result, result); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	}
}

// isServerRequest reports whether the message is a request from the server,
// which awaits a response: it has a method and an id. A notification has no
// id.
func (m rpcMessage) isServerRequest() bool {
	return m.Method != "" && len(m.ID) > 0 && string(m.ID) != "null"
}

// answers reports whether the message is the response to request id. The id
// matches by value, whether the server writes it as a number or a string.
func (m rpcMessage) answers(id int) bool {
	if m.Method != "" {
		return false
	}
	var number int
	if json.Unmarshal(m.ID, &number) == nil {
		return number == id
	}
	var text string
	return json.Unmarshal(m.ID, &text) == nil && text == strconv.Itoa(id)
}

func (c *appServerClient) send(message any) error {
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	return c.session.Send(string(encoded))
}

func (m appServerModel) providerModel() (core.ProviderModel, bool) {
	id := strings.TrimSpace(m.Model)
	if m.Hidden || id == "" {
		return core.ProviderModel{}, false
	}

	displayName := strings.TrimSpace(m.DisplayName)
	if displayName == "" {
		displayName = id
	}

	var levels []string
	for _, effort := range m.SupportedReasoningEfforts {
		if level := strings.TrimSpace(effort.ReasoningEffort); level != "" {
			levels = append(levels, level)
		}
	}

	return core.ProviderModel{ID: id, DisplayName: displayName, ReasoningLevels: levels}, true
}
