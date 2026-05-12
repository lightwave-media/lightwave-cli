package memory

import (
	"errors"
	"strings"
	"testing"
)

func pinHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestPutGet_RoundTrip(t *testing.T) {
	pinHome(t)
	if _, err := Put("v_core", "sprint-state", []byte("active=SPR-001")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := Get("v_core", "sprint-state")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "active=SPR-001" {
		t.Errorf("Get = %q", got)
	}
}

func TestGet_NotFound(t *testing.T) {
	pinHome(t)
	_, err := Get("v_core", "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList_SortedAndSkipsTmp(t *testing.T) {
	pinHome(t)
	for _, k := range []string{"b", "a", "c"} {
		if _, err := Put("ns", k, []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := List("ns")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("List len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("List[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestList_MissingNamespace(t *testing.T) {
	pinHome(t)
	got, err := List("never-written")
	if err != nil {
		t.Fatalf("List on missing namespace should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List on missing namespace = %v, want []", got)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	pinHome(t)
	if _, err := Put("ns", "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := Delete("ns", "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Second delete should not error.
	if err := Delete("ns", "k"); err != nil {
		t.Fatalf("Delete (idempotent): %v", err)
	}
	if _, err := Get("ns", "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete, expected ErrNotFound, got %v", err)
	}
}

func TestHierarchicalKey(t *testing.T) {
	pinHome(t)
	if _, err := Put("v_core", "tasks/T-0001/status", []byte("in_progress")); err != nil {
		t.Fatalf("Put hierarchical: %v", err)
	}
	got, err := Get("v_core", "tasks/T-0001/status")
	if err != nil {
		t.Fatalf("Get hierarchical: %v", err)
	}
	if string(got) != "in_progress" {
		t.Errorf("Get = %q", got)
	}

	// List should surface the hierarchical key with its full relative path.
	keys, err := List("v_core")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "tasks/T-0001/status" {
		t.Errorf("List = %v, want [tasks/T-0001/status]", keys)
	}
}

func TestValidation_TraversalRejected(t *testing.T) {
	cases := []struct {
		ns, key string
	}{
		{"..", "k"},
		{".", "k"},
		{"ns", ".."},
		{"ns", "."},
		{"ns", "a/../b"},
		{"ns", "/leading"},
		{"ns", "trailing/"},
		{"ns/with/slash", "k"},
		{"", "k"},
		{"ns", ""},
	}
	for _, tc := range cases {
		_, err := Put(tc.ns, tc.key, []byte("x"))
		if err == nil {
			t.Errorf("Put(%q, %q) should have errored", tc.ns, tc.key)
		}
	}
}

func TestNamespaces(t *testing.T) {
	pinHome(t)
	for _, ns := range []string{"v_core", "cpe", "cqa"} {
		if _, err := Put(ns, "k", []byte("v")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Namespaces()
	if err != nil {
		t.Fatalf("Namespaces: %v", err)
	}
	if strings.Join(got, ",") != "cpe,cqa,v_core" {
		t.Errorf("Namespaces = %v, want sorted [cpe cqa v_core]", got)
	}
}
