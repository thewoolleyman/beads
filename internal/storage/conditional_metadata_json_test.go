package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConditionalMetadataInput(t *testing.T) {
	valid := `{"operation_id":"one","expected_metadata":{},"metadata":{"large":9007199254740993},"comments":["handoff"]}`
	r, err := DecodeConditionalMetadataRequest([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	obj, err := StrictJSONObject(r.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if obj["large"] != json.Number("9007199254740993") {
		t.Fatal("large integer lost precision", obj)
	}
	for _, bad := range []string{
		strings.Replace(valid, `"one"`, `"one","operation_id":"two"`, 1),
		strings.Replace(valid, `9007199254740993`, `1,"large":2`, 1),
		valid + ` {}`, strings.Replace(valid, `"comments"`, `"unknown"`, 1),
		strings.Replace(valid, `"expected_metadata":{}`, `"expected_metadata":null`, 1),
		strings.Replace(valid, `"large":9007199254740993`, `"_bd_conditional_revision":"invented"`, 1),
		strings.Replace(valid, `"one"`, `" "`, 1), strings.Repeat(" ", ConditionalMetadataMaxBytes) + valid,
	} {
		if _, err := DecodeConditionalMetadataRequest([]byte(bad)); err == nil {
			t.Errorf("accepted invalid input %.100s", bad)
		}
	}
}
