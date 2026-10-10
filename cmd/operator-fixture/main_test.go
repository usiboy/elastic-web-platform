package main

import "testing"

func TestOperatorFixtureRequiresSeparateScopesAndPinnedInputs(t *testing.T) {
	valid:=config{databaseURL:"postgres://localhost/test",artifactDir:"/tmp/artifacts",teamID:"t_alpha",projectID:"p_alpha",host:"a.preview.ecp.lingtong.com",sourceFile:"/tmp/a-source.tar.gz",builtFile:"/tmp/a-built.tar.gz",sourceSHA:"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",builtSHA:"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",builderRef:"operator-fixture:node22-vite7"}
	if err:=validateConfig(valid); err!=nil { t.Fatal(err) }
	bad:=valid; bad.artifactDir=""; if err:=validateConfig(bad); err==nil { t.Fatal("missing independent artifact store accepted") }
	bad=valid; bad.host="b.preview.ecp.lingtong.com/path"; if err:=validateConfig(bad); err==nil { t.Fatal("invalid Host accepted") }
	bad=valid; bad.builderRef="signed-builder"; if err:=validateConfig(bad); err==nil { t.Fatal("operator claim misrepresented as signed builder") }
}

func TestOperatorFixtureAcceptsVerifiedTestBMetadata(t *testing.T) {
	b:=config{
		databaseURL:"postgres://localhost/test",artifactDir:"/data/ewp-preview-20261006/artifacts",
		teamID:"t_eee8046ca49c271f5bea06acf285be66",projectID:"p_e5123bae12388454bb82fc1590750ab9",
		host:"b.preview.ecp.lingtong.com",
		sourceFile:"/data/ewp-preview-20261006/state/fixture-inputs/repo-1404606859-9ea1fc0c7037.tar.gz",
		builtFile:"/data/ewp-preview-20261006/state/fixture-inputs/testB-built-no-treeshake-9ea1fc0c7037.tar.gz",
		sourceSHA:"044e7ed94cdcb02c1b1ff6e6654a12ba8e89d46d9eda7e55ce7eaf5cd30d906a",
		builtSHA:"92d909ef69e684f09fcdd82ed88468e7181ce74a2fec5c69547db1818effe712",
		builderRef:"operator-fixture:node22-vite7-no-treeshake",
	}
	if err:=validateConfig(b); err!=nil { t.Fatalf("testB verified metadata rejected: %v",err) }
}
