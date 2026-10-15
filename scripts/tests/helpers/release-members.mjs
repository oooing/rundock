import { readFileSync, readdirSync } from 'node:fs'
import assert from 'node:assert/strict'
import ts from 'typescript'
import * as vue from 'vue'

// Load named assignments from the current controllers. Fail if a member moves
// or disappears; never fall back to a copied implementation of the old modal.
const directory = new URL('../../../src/components/release/', import.meta.url)
const members = new Map()
for (const file of readdirSync(directory).filter(name => name.endsWith('.ts'))) {
  const ast = ts.createSourceFile(file, readFileSync(new URL(file, directory), 'utf8'), ts.ScriptTarget.Latest, true)
  for (const installer of ast.statements.filter(ts.isFunctionDeclaration)) {
    for (const statement of installer.body?.statements || []) {
      const expr = statement.expression
      if (expr && ts.isBinaryExpression(expr) && expr.operatorToken.kind === ts.SyntaxKind.EqualsToken
          && ts.isPropertyAccessExpression(expr.left) && expr.left.expression.getText(ast) === 'ctx') {
        members.set(expr.left.name.text, statement.getText(ast))
      }
    }
  }
}
export function loadMembers(names, ctx, dependencies = {}) {
  const source = names.map(name => { assert.ok(members.has(name), `Missing current controller member: ${name}`); return members.get(name) }).join('\n')
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText
  const deps = { computed: vue.computed, ref: vue.ref, watch: vue.watch, nextTick: vue.nextTick, tr: text => text, ...dependencies }
  new Function('ctx', ...Object.keys(deps), code)(ctx, ...Object.values(deps))
  return Object.fromEntries(names.map(name => [name, ctx[name]]))
}
