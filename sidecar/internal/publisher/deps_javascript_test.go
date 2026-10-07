package publisher

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocalJSImportsUseExecutableTokens(t *testing.T) {
	cases := []struct {
		name, file, source string
		want               []string
	}{
		{"static", "entry.js", `import './side.js'; import A from "./A.vue"; export { x } from '../x.ts'; export * from './all.js'; const helper = require('./helper.cjs');`, []string{"./side.js", "./A.vue", "../x.ts", "./all.js", "./helper.cjs"}},
		{"strings", "entry.mjs", `writeFileSync('entry.js', "import Fixture from './Fixture.vue'; require('./generated.js')"); const quote = 'export * from "./fake.js"'; import './real.js';`, []string{"./real.js"}},
		{"escaped quotes", "entry.js", `const sample = "quoted \" import './fake.js'"; const other = 'quoted \' require("./fake.cjs")'; import './real.js';`, []string{"./real.js"}},
		{"comments", "entry.ts", "// import './line.js'\n/* export * from './block.js'; require('./comment.cjs') */\nimport /* explanation */ {\n x, y as z\n} from /* path */ './real.ts'", []string{"./real.ts"}},
		{"templates", "entry.js", "const fixture = `import './text.js'; ${require('./helper.js')} ${`nested import './nested.js'; ${import('./lazy.js')}`} end`;", []string{"./helper.js", "./lazy.js"}},
		{"escaped template", "entry.js", "const fixture = `escaped \\` import './fake.js'; \\${require('./fake.cjs')}`; import './real.js';", []string{"./real.js"}},
		{"dynamic literals", "entry.ts", `const a = import('./lazy.js'); const b = import('./data.json', { with: { type: 'json' } }); const c = import('./prefix/' + name); const d = require('./prefix/' + name);`, []string{"./lazy.js", "./data.json"}},
		{"member calls", "entry.js", `obj.require('./method.js'); obj?.require('./optional.js'); obj.import('./also-method.js'); const myrequire = () => ''; myrequire('./not-a-module.js'); import './real.js';`, []string{"./real.js"}},
		{"type imports", "entry.ts", "import type {\n Props\n} from './types'; export type { Props } from './exported'; import helper = require('./helper.cjs');", []string{"./types", "./exported", "./helper.cjs"}},
		{"regexp", "entry.js", `const re = /import fake from '.\/fake.js'/; if (ok) /require\('.\/also-fake.js'\)/.test(text); const ratio = n / require('./divisor.js'); import './real.js';`, []string{"./divisor.js", "./real.js"}},
		{"not across statements", "entry.js", "export default value;\nconst from = './not-a-module.js'; import './real.js';", []string{"./real.js"}},
		{"jsx", "entry.jsx", `import View from './View.jsx'; const el = <div title="import './fake.js'">hello</div>; const helper = require('./helper.js');`, []string{"./View.jsx", "./helper.js"}},
		{"vue scripts only", "Widget.vue", `<!-- <script>import './commented.js'</script> --><template><pre>import './example.js'</pre></template><script setup lang="ts">import type { Props } from './types'; const example = "require('./fake.js')";</script><style>/* import './style-example.js' */</style><script>export { x } from './other.js'</script>`, []string{"./types", "./other.js"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := localImportSpecs(tc.file, tc.source)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("imports = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDependencyCheckGeneratedFixtureDoesNotBlockButRealMissingFileDoes(t *testing.T) {
	root := t.TempDir()
	rel := "scripts/acceptance/release-settings.mjs"
	source := "import { writeFileSync } from 'node:fs';\n" +
		"writeFileSync('outputs/fixture/Fixture.vue', `<template>fixture</template>`);\n" +
		`writeFileSync('outputs/fixture/entry.js', "import Fixture from './Fixture.vue';");`
	writeTestFile(t, filepath.Join(root, rel), source)
	if got := findMissingLocalDependencies(root, root, []string{rel}, nil); len(got) != 0 {
		t.Fatalf("generated source created false dependencies: %+v", got)
	}
	writeTestFile(t, filepath.Join(root, rel), source+"\nimport './real-helper.js';")
	got := findMissingLocalDependencies(root, root, []string{rel}, nil)
	if len(got) != 1 || !got[0].Blocked || got[0].Missing != "scripts/acceptance/real-helper.js" {
		t.Fatalf("real missing dependency must still block: %+v", got)
	}
}

func TestDependencyScanActualAcceptanceFixtureGenerator(t *testing.T) {
	// This optional source-level regression supplements the self-contained
	// fixture above when the UI acceptance scripts are present in the checkout.
	script := filepath.Join("..", "..", "..", "scripts", "acceptance", "release-settings.mjs")
	raw, err := os.ReadFile(script)
	if os.IsNotExist(err) {
		t.Skip("UI acceptance scripts not included in this checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	if got := localImportSpecs("release-settings.mjs", string(raw)); len(got) != 0 {
		t.Fatalf("fixture generator has no real relative imports: %q", got)
	}
}
