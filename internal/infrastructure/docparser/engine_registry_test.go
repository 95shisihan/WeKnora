package docparser

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type externalTestEngine struct{ name string }

func (e externalTestEngine) Name() string                                        { return e.name }
func (externalTestEngine) Description() string                                   { return "external" }
func (externalTestEngine) FileTypes(bool) []string                               { return []string{"test"} }
func (externalTestEngine) CheckAvailable(bool, map[string]string) (bool, string) { return true, "" }
func (externalTestEngine) NewReader(context.Context, ReaderDeps) (interfaces.DocReader, error) {
	return nil, nil
}

func TestListAllEnginesBuiltinIncludesDocumentFormats(t *testing.T) {
	engines := ListAllEngines(true, nil, nil)
	for _, engine := range engines {
		if engine.Name != "builtin" {
			continue
		}
		if !engine.Available {
			t.Fatalf("builtin engine is unavailable: %s", engine.UnavailableReason)
		}

		fileTypes := make(map[string]bool, len(engine.FileTypes))
		for _, fileType := range engine.FileTypes {
			fileTypes[fileType] = true
		}
		for _, want := range []string{"html", "htm", "xmind"} {
			if !fileTypes[want] {
				t.Errorf("builtin engine file types do not include %q: %v", want, engine.FileTypes)
			}
		}
		return
	}

	t.Fatal("builtin engine not found")
}

func TestExternalEngineCanBeRegisteredAndRemoved(t *testing.T) {
	engine := externalTestEngine{name: "external_registry_test"}
	if err := RegisterExternalEngine(engine); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = UnregisterExternalEngine(engine.name) })
	if _, ok := lookupEngine(engine.name); !ok {
		t.Fatal("registered external engine was not found")
	}
	if err := RegisterExternalEngine(engine); err == nil {
		t.Fatal("duplicate external engine registration succeeded")
	}
	if err := UnregisterExternalEngine(engine.name); err != nil {
		t.Fatal(err)
	}
	if _, ok := lookupEngine(engine.name); ok {
		t.Fatal("external engine remains after unregister")
	}
}
