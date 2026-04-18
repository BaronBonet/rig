package main

import "testing"

func TestDebugMode_DefaultIsSupported(t *testing.T) {
	switch debugMode {
	case debugModeCreate, debugModeSubscribe:
	default:
		t.Fatalf("unsupported debugMode default %q", debugMode)
	}
}

func TestDebugCreateStatusStreaming_DefaultsToNoTimeout(t *testing.T) {
	if debugStatusObserver.StatusWaitAfter != 0 {
		t.Fatalf("expected create-mode status streaming to stay open until cancelled, got %s", debugStatusObserver.StatusWaitAfter)
	}
}
