package codexhooks

import (
	"time"

	codexagent "rig/internal/adapters/client/codexagent"
	"rig/internal/core"
)

type HookEventIngestor = codexagent.HookEventIngestor

type HTTPHandler = codexagent.HTTPHandler

var NewHTTPHandler = codexagent.NewHTTPHandler

func DecodeHookEventInput(now func() time.Time, headerEventName string, body []byte) core.HookEventInput {
	return codexagent.DecodeHookEventInput(now, headerEventName, body)
}
