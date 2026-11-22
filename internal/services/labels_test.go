package services

import (
	"reflect"
	"testing"
)

func TestParseNodeLabels(t *testing.T) {
	got := parseNodeLabels(" vm-host=5c1e , vm-name=vm-01,,bad, =x ")
	want := map[string]string{"vm-host": "5c1e", "vm-name": "vm-01"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNodeLabels = %v, want %v", got, want)
	}
	if len(parseNodeLabels("")) != 0 {
		t.Fatal("empty NODE_LABELS must give no labels")
	}
}
