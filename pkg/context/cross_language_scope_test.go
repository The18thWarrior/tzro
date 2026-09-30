package context

import (
	stdctx "context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestFileScopeAcrossLanguages(t *testing.T) {
	cases := []struct {
		name, ext, source, caller, unrelated string
		want                                 []string
	}{
		{"go", ".go", "package p\nvar Global int; func Chosen(){var Local int; _=Local}; type PublicType int\nvar Closure=func(){type Hidden int}\nfunc (p PublicType) Member(){const HiddenMember=1}\n", "package p\nfunc Consumer(){Chosen()}\n", "package p\nfunc Other(){Local()}\n", []string{"Chosen", "Closure", "Global", "Member", "PublicType"}},
		{"python", ".py", "def Chosen():\n def Local(): pass\n class Hidden: pass\nclass PublicType:\n def Member(self):\n  def HiddenMember(): pass\n", "def Consumer():\n Chosen()\n", "def Other():\n Local()\n", []string{"Chosen", "Member", "PublicType"}},
		{"javascript", ".js", "export function Chosen(){function Local(){}; class Hidden{method(){}}}\nexport const Closure=()=>{function HiddenClosure(){}}; class PublicType {Member(){function HiddenMember(){}}}\n", "function Consumer(){Chosen()}\n", "function Other(){Local()}\n", []string{"Chosen", "Closure", "Member", "PublicType"}},
		{"typescript", ".ts", "export function Chosen(){function Local(){}; type Hidden=string}\nexport const Closure=()=>{function HiddenClosure(){}}; class PublicType {Member(){function HiddenMember(){}}}\ninterface Contract{}; type Alias=string\n", "function Consumer(){Chosen()}\n", "function Other(){Local()}\n", []string{"Alias", "Chosen", "Closure", "Contract", "Member", "PublicType"}},
		{"tsx", ".tsx", "export function Chosen(){function Local(){}; return <div/>}\nexport const Closure=()=>{function HiddenClosure(){}; return <span/>}; class PublicType {Member(){function HiddenMember(){}}}\n", "function Consumer(){Chosen()}\n", "function Other(){Local()}\n", []string{"Chosen", "Closure", "Member", "PublicType"}},
		{"rust", ".rs", "pub fn Chosen(){fn Local(){} struct Hidden;}\npub struct PublicType; impl PublicType {pub fn Member(){fn HiddenMember(){}}}\nmod module {pub fn Visible(){fn HiddenModule(){}}}\nstatic C: fn() = || {fn HiddenClosure(){}};\n", "fn Consumer(){Chosen();}\n", "fn Other(){Local();}\n", []string{"Chosen", "Member", "PublicType", "Visible"}},
		{"javascript_expression", ".js", "const Factory = function(){function Local(){};}; function Chosen(){}\n", "function Consumer(){Chosen()}\n", "function Other(){Local()}\n", []string{"Chosen"}},
		{"typescript_members", ".ts", "class PublicType {field=()=>{function Local(){}}; get value(){function HiddenGetter(){}}; set value(v){function HiddenSetter(){}}}\nfunction Chosen(){}\n", "function Consumer(){Chosen()}\n", "function Other(){Local()}\n", []string{"Chosen", "PublicType", "value", "value"}},
	}
	rows := []map[string]any{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.ext == ".rs" {
				if err := os.Mkdir(filepath.Join(dir, "src"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "src", "lib.rs"), []byte("#[path=\"../chosen.rs\"] pub mod chosen;\n#[path=\"../caller.rs\"] mod caller;\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname=\"scope_fixture\"\nversion=\"0.1.0\"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			file := "chosen" + tc.ext
			for name, body := range map[string]string{file: tc.source, "caller" + tc.ext: tc.caller, "unrelated" + tc.ext: tc.unrelated} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			report, _, err := NewImpactAnalyzer(nil, nil).AnalyzeFiles(stdctx.Background(), dir, []string{file}, 4000, false)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, d := range report.ChangedSymbols {
				names = append(names, d.Name)
			}
			sort.Strings(names)
			sort.Strings(tc.want)
			caller, unrelated := false, false
			for _, edge := range report.ReferenceEdges {
				if edge.FilePath == "caller"+tc.ext {
					caller = true
				}
				if edge.FilePath == "unrelated"+tc.ext {
					unrelated = true
				}
			}
			legacy, err := extractDeclarationsFromAST(file, []byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			legacyNames := []string{}
			for _, d := range legacy {
				legacyNames = append(legacyNames, d.Name)
			}
			sort.Strings(legacyNames)
			hasLocal := false
			for _, n := range legacyNames {
				if n == "Local" {
					hasLocal = true
				}
			}
			pass := reflect.DeepEqual(names, tc.want) && caller && !unrelated && hasLocal
			rows = append(rows, map[string]any{"case": tc.name, "anchors": names, "expected": tc.want, "legacy_anchors": legacyNames, "true_caller": caller, "unrelated_caller": unrelated, "pass": pass})
			if !pass {
				t.Errorf("anchors=%v expected=%v caller=%v unrelated=%v diffKeepsLocal=%v", names, tc.want, caller, unrelated, hasLocal)
			}
		})
	}
	if path := os.Getenv("TZRO_SCOPE_MATRIX"); path != "" {
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
