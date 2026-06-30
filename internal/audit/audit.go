// Package audit centralizes the maintenance audit trail. Every significant
// transition is recorded as a structured log line and, optionally, as a
// Kubernetes Event on the owning object and/or as a JSON line appended to a
// file (e.g. a mounted PVC) for long-term retention.
package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// Audit action identifiers, recorded as the JSON "action" and used as the
// Kubernetes Event reason (in CamelCase form via reasonFor).
const (
	ActionCreated            = "request.created"
	ActionPreflightCompleted = "preflight.completed"
	ActionApprovalRequested  = "approval.requested"
	ActionApprovalGranted    = "approval.granted"
	ActionApprovalDenied     = "approval.denied"
	ActionPlanGenerated      = "plan.generated"
	ActionNodeCordoned       = "node.cordoned"
	ActionPodEvicted         = "pod.evicted"
	ActionNodeDrained        = "node.drained"
	ActionNodeUncordoned     = "node.uncordoned"
	ActionNodeReplacing      = "node.replacing"
	ActionNodeReplaced       = "node.replaced"
	ActionNodeBlocked        = "node.blocked"
	ActionPaused             = "request.paused"
	ActionResumed            = "request.resumed"
	ActionBlocked            = "request.blocked"
	ActionCompleted          = "request.completed"
	ActionFailed             = "request.failed"
	ActionCancelled          = "request.cancelled"
)

// Logger writes audit records to logs, Kubernetes Events and an optional file.
type Logger struct {
	log      logr.Logger
	recorder record.EventRecorder
	events   bool

	mu   sync.Mutex
	file *os.File
}

// entry is the JSON shape written to the export file.
type entry struct {
	Time      time.Time         `json:"time"`
	Level     string            `json:"level"`
	Action    string            `json:"action"`
	Message   string            `json:"message"`
	Kind      string            `json:"kind,omitempty"`
	Namespace string            `json:"namespace,omitempty"`
	Name      string            `json:"name,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// New constructs a Logger. If exportPath is non-empty, audit records are also
// appended as JSON lines to that file. The caller must Close the Logger.
func New(log logr.Logger, recorder record.EventRecorder, enableEvents bool, exportPath string) (*Logger, error) {
	l := &Logger{
		log:      log.WithName("audit"),
		recorder: recorder,
		events:   enableEvents,
	}
	if exportPath != "" {
		f, err := os.OpenFile(exportPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return nil, fmt.Errorf("open audit export file %q: %w", exportPath, err)
		}
		l.file = f
	}
	return l, nil
}

// Record writes one audit record. eventType is corev1.EventTypeNormal or
// corev1.EventTypeWarning. obj may be nil to skip the Kubernetes Event.
func (l *Logger) Record(obj runtime.Object, eventType, action, message string, fields map[string]string) {
	kvs := flatten(action, eventType, fields)
	l.log.Info(message, kvs...)

	if l.events && l.recorder != nil && obj != nil {
		l.recorder.Event(obj, eventType, reasonFor(action), message)
	}

	if l.file != nil {
		l.writeFile(obj, eventType, action, message, fields)
	}
}

func (l *Logger) writeFile(obj runtime.Object, eventType, action, message string, fields map[string]string) {
	e := entry{
		Time:    time.Now().UTC(),
		Level:   eventType,
		Action:  action,
		Message: message,
		Fields:  fields,
	}
	if obj != nil {
		if accessor, err := meta.Accessor(obj); err == nil {
			e.Namespace = accessor.GetNamespace()
			e.Name = accessor.GetName()
		}
		e.Kind = obj.GetObjectKind().GroupVersionKind().Kind
	}

	data, err := json.Marshal(e)
	if err != nil {
		l.log.Error(err, "failed to marshal audit entry", "action", action)
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.Write(append(data, '\n')); err != nil {
		l.log.Error(err, "failed to write audit entry", "action", action)
	}
}

// Close flushes and closes the export file, if any.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

func flatten(action, eventType string, fields map[string]string) []any {
	kvs := []any{"action", action, "eventType", eventType}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kvs = append(kvs, k, fields[k])
	}
	return kvs
}

// reasonFor converts a dotted action ("node.cordoned") into a CamelCase
// Kubernetes Event reason ("NodeCordoned").
func reasonFor(action string) string {
	parts := splitDots(action)
	out := make([]byte, 0, len(action))
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, upperFirst(p)...)
	}
	if len(out) == 0 {
		return "Maintenance"
	}
	return string(out)
}

func splitDots(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}
