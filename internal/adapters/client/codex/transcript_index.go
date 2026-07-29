package codex

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/BaronBonet/rig/internal/core"
)

const (
	defaultCodexTranscriptIndexBudget = 64 << 20
	transcriptIndexEntryOverhead      = 256
	transcriptActivityOverhead        = 96
	transcriptReadBufferSize          = 64 << 10
)

type codexTranscriptIndex struct {
	mu       sync.Mutex
	entries  map[string]*codexTranscriptEntry
	lru      *list.List
	maxBytes int64
	used     int64
	metrics  codexTranscriptIndexMetrics
}

type codexTranscriptIndexMetrics struct {
	cacheHits atomic.Uint64
	rebuilds  atomic.Uint64
	bytesRead atomic.Uint64
	evictions atomic.Uint64
}

type codexTranscriptIndexStats struct {
	CacheHits uint64
	Rebuilds  uint64
	BytesRead uint64
	Evictions uint64
}

type codexTranscriptEntry struct {
	mu sync.Mutex

	path       string
	element    *list.Element
	users      int
	weight     int64
	identity   os.FileInfo
	offset     int64
	trailing   []byte
	status     *codexTranscriptStatus
	usage      *core.SessionTokenUsage
	activities []core.TaskActivityEvent
}

type codexTranscriptSnapshot struct {
	status     *codexTranscriptStatus
	usage      *core.SessionTokenUsage
	activities []core.TaskActivityEvent
}

func newCodexTranscriptIndex(maxBytes int64) *codexTranscriptIndex {
	if maxBytes <= 0 {
		maxBytes = defaultCodexTranscriptIndexBudget
	}
	return &codexTranscriptIndex{
		entries:  make(map[string]*codexTranscriptEntry),
		lru:      list.New(),
		maxBytes: maxBytes,
	}
}

func (r *repository) getTranscriptIndex() *codexTranscriptIndex {
	r.transcriptIndexOnce.Do(func() {
		if r.transcripts == nil {
			r.transcripts = newCodexTranscriptIndex(defaultCodexTranscriptIndexBudget)
		}
	})
	return r.transcripts
}

func (r *repository) transcriptStats() codexTranscriptIndexStats {
	return r.getTranscriptIndex().stats()
}

func (i *codexTranscriptIndex) stats() codexTranscriptIndexStats {
	return codexTranscriptIndexStats{
		CacheHits: i.metrics.cacheHits.Load(),
		Rebuilds:  i.metrics.rebuilds.Load(),
		BytesRead: i.metrics.bytesRead.Load(),
		Evictions: i.metrics.evictions.Load(),
	}
}

func (i *codexTranscriptIndex) read(
	ctx context.Context,
	path string,
) (codexTranscriptSnapshot, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return codexTranscriptSnapshot{}, nil
	}

	entry := i.acquire(path)
	defer i.release(entry)

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if err := i.refreshLocked(ctx, entry); err != nil {
		return codexTranscriptSnapshot{}, err
	}
	return snapshotTranscriptEntry(entry), nil
}

func (i *codexTranscriptIndex) acquire(path string) *codexTranscriptEntry {
	i.mu.Lock()
	defer i.mu.Unlock()
	if entry := i.entries[path]; entry != nil {
		entry.users++
		i.lru.MoveToFront(entry.element)
		i.metrics.cacheHits.Add(1)
		return entry
	}

	entry := &codexTranscriptEntry{
		path:   path,
		users:  1,
		weight: transcriptIndexEntryOverhead + int64(len(path)),
	}
	entry.element = i.lru.PushFront(entry)
	i.entries[path] = entry
	i.used += entry.weight
	return entry
}

func (i *codexTranscriptIndex) release(entry *codexTranscriptEntry) {
	i.mu.Lock()
	defer i.mu.Unlock()
	entry.users--
	i.evictLocked()
}

func (i *codexTranscriptIndex) updateWeight(entry *codexTranscriptEntry, weight int64) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.used += weight - entry.weight
	entry.weight = weight
	i.evictLocked()
}

func (i *codexTranscriptIndex) evictLocked() {
	for i.used > i.maxBytes {
		var candidate *codexTranscriptEntry
		for element := i.lru.Back(); element != nil; element = element.Prev() {
			entry := element.Value.(*codexTranscriptEntry)
			if entry.users == 0 {
				candidate = entry
				break
			}
		}
		if candidate == nil {
			return
		}
		delete(i.entries, candidate.path)
		i.lru.Remove(candidate.element)
		i.used -= candidate.weight
		i.metrics.evictions.Add(1)
	}
}

func (i *codexTranscriptIndex) refreshLocked(ctx context.Context, entry *codexTranscriptEntry) error {
	file, err := os.Open(entry.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open transcript %q: %w", entry.path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat transcript %q: %w", entry.path, err)
	}

	previousSize := entry.offset + int64(len(entry.trailing))
	rebuild := entry.identity == nil ||
		!os.SameFile(entry.identity, info) ||
		info.Size() < previousSize ||
		(info.Size() == previousSize && !info.ModTime().Equal(entry.identity.ModTime()))

	offset := entry.offset
	trailing := append([]byte(nil), entry.trailing...)
	status := cloneCodexTranscriptStatus(entry.status)
	usage := cloneSessionTokenUsage(entry.usage)
	activities := append([]core.TaskActivityEvent(nil), entry.activities...)
	if rebuild {
		offset = 0
		trailing = nil
		status = nil
		usage = nil
		activities = nil
	}

	readOffset := offset + int64(len(trailing))
	if _, err := file.Seek(readOffset, io.SeekStart); err != nil {
		return fmt.Errorf("seek transcript %q: %w", entry.path, err)
	}
	appended, err := readTranscriptAppend(ctx, file, &i.metrics)
	if err != nil {
		return err
	}

	pending := append(trailing, appended...)
	consumed := 0
	for {
		lineEnd := bytes.IndexByte(pending[consumed:], '\n')
		if lineEnd < 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		lineEnd += consumed
		parseCodexTranscriptRecord(pending[consumed:lineEnd], &status, &usage, &activities)
		consumed = lineEnd + 1
	}

	offset += int64(consumed)
	trailing = append([]byte(nil), pending[consumed:]...)
	entry.identity = info
	entry.offset = offset
	entry.trailing = trailing
	entry.status = status
	entry.usage = usage
	entry.activities = activities
	if rebuild {
		i.metrics.rebuilds.Add(1)
	}
	i.updateWeight(entry, transcriptEntryWeight(entry))
	return nil
}

func readTranscriptAppend(
	ctx context.Context,
	reader io.Reader,
	metrics *codexTranscriptIndexMetrics,
) ([]byte, error) {
	var appended []byte
	buffer := make([]byte, transcriptReadBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, err := reader.Read(buffer)
		if count > 0 {
			appended = append(appended, buffer[:count]...)
			metrics.bytesRead.Add(uint64(count))
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return appended, nil
		}
		return nil, err
	}
}

func parseCodexTranscriptRecord(
	line []byte,
	status **codexTranscriptStatus,
	usage **core.SessionTokenUsage,
	activities *[]core.TaskActivityEvent,
) {
	var envelope codexTranscriptEnvelope
	if err := jsonUnmarshalTranscriptLine(line, &envelope); err != nil {
		return
	}

	if candidate := codexTranscriptEnvelopeStatus(envelope); candidate != nil &&
		(*status == nil || candidate.observedAt.After((*status).observedAt)) {
		*status = candidate
	}
	if candidate := codexTranscriptEnvelopeTokenUsage(envelope); candidate != nil {
		*usage = candidate
	}
	if activity := codexTranscriptActivityEvent("", envelope); activity != nil {
		*activities = append(*activities, *activity)
	}
}

func jsonUnmarshalTranscriptLine(line []byte, target any) error {
	return json.Unmarshal(bytes.TrimSpace(line), target)
}

func codexTranscriptEnvelopeStatus(envelope codexTranscriptEnvelope) *codexTranscriptStatus {
	if envelope.Type != "event_msg" || envelope.Timestamp.IsZero() || len(envelope.Payload) == 0 {
		return codexResponseItemStatus(envelope)
	}

	var payload codexTaskCompletePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return nil
	}
	return codexEventMessageStatus(envelope.Timestamp, payload.Type)
}

func codexTranscriptEnvelopeTokenUsage(envelope codexTranscriptEnvelope) *core.SessionTokenUsage {
	if envelope.Type != "event_msg" || len(envelope.Payload) == 0 {
		return nil
	}
	var payload codexEventPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.Type != "token_count" {
		return nil
	}

	total := payload.Info.TotalTokenUsage
	if total.TotalTokens == 0 && total.InputTokens == 0 && total.OutputTokens == 0 {
		return nil
	}
	return &core.SessionTokenUsage{
		InputTokens:              total.InputTokens,
		OutputTokens:             total.OutputTokens,
		CachedInputTokens:        total.CachedInputTokens,
		CacheCreationInputTokens: total.CacheCreationInputTokens,
		ReasoningOutputTokens:    total.ReasoningOutputTokens,
		TotalTokens:              total.TotalTokens,
	}
}

func snapshotTranscriptEntry(entry *codexTranscriptEntry) codexTranscriptSnapshot {
	return codexTranscriptSnapshot{
		status:     cloneCodexTranscriptStatus(entry.status),
		usage:      cloneSessionTokenUsage(entry.usage),
		activities: append([]core.TaskActivityEvent(nil), entry.activities...),
	}
}

func transcriptEntryWeight(entry *codexTranscriptEntry) int64 {
	weight := int64(transcriptIndexEntryOverhead + len(entry.path) + len(entry.trailing))
	for _, activity := range entry.activities {
		weight += int64(
			transcriptActivityOverhead +
				len(activity.EventName) +
				len(activity.Role) +
				len(activity.Text),
		)
	}
	return weight
}

func cloneCodexTranscriptStatus(status *codexTranscriptStatus) *codexTranscriptStatus {
	if status == nil {
		return nil
	}
	copyStatus := *status
	return &copyStatus
}

func cloneSessionTokenUsage(usage *core.SessionTokenUsage) *core.SessionTokenUsage {
	if usage == nil {
		return nil
	}
	copyUsage := *usage
	return &copyUsage
}
