package modelconnector

import (
	"testing"
)

func TestCodexRejectsFailureAndHostTool(t *testing.T) {
	for _, s := range []string{`{"type":"turn.failed"}`, `{"type":"item.completed","item":{"type":"command_execution"}}` + "\n" + `{"type":"turn.completed"}`, `{"type":"item.completed","item":{"type":"agent_message"}}`} {
		if ValidateCodexEvents([]byte(s)) == nil {
			t.Fatalf("accepted invalid stream %s", s)
		}
	}
	if e := ValidateCodexEvents([]byte(`{"type":"turn.completed","usage":{"output_tokens":1}}`)); e != nil {
		t.Fatal(e)
	}
}
func TestWorkBuddyFinalOnly(t *testing.T) {
	ok := `{"type":"assistant"}` + "\n" + `{"type":"result","is_error":false,"structured_output":{"tool":"finish_task","observation_id":"o1","action":null,"reason":"candidate"}}`
	r, e := ParseWorkBuddy([]byte(ok))
	if e != nil || r.Decision.ObservationID != "o1" {
		t.Fatalf("%+v %v", r, e)
	}
	for _, bad := range []string{`{"type":"result","is_error":true}`, `{"type":"result","result":"done"}`, ok + "\n" + ok} {
		if _, e = ParseWorkBuddy([]byte(bad)); e == nil {
			t.Fatal("accepted failed or duplicate result")
		}
	}
}
