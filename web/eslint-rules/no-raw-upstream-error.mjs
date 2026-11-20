// #2737 (was the #2645 regex sentinel) — an error response's text goes
// through `upstreamErrorMessage` / `upstreamErrorText` (lib/api/_shared.ts),
// which pass a 4xx `{error}` through and show generic text for a 5xx. A
// hand-rolled read of the body's `error` field shows a 5xx body too, so a
// handler that wrote `err.Error()` there would leak internals (#2433).
//
// `res.json()` is `Promise<any>`, so an uncast read carries no type to match
// on. This rule follows the value instead: an `error` read (member access or
// destructuring) is flagged when the object is a parsed body, meaning
// `x.json()` with no arguments or `JSON.parse(…)`, seen through `await`,
// casts, `.catch(…)`, `||`/`??` defaults and local variables bound to one.
//
// It also keeps the three syntactic shapes the regex caught, for a body that
// arrives as a parameter and cannot be traced: a cast to `{ error?: … }`,
// `& { error?: … }`, and `.error ?? \`…${res.status}…\``.
//
// The helper's own file and tests are excluded in eslint.config.mjs; there
// is no other allow-list.

const TS_WRAPPERS = new Set([
  "TSAsExpression",
  "TSSatisfiesExpression",
  "TSNonNullExpression",
  "TSTypeAssertion",
  "AwaitExpression",
  "ChainExpression",
]);

function unwrap(node) {
  while (node && TS_WRAPPERS.has(node.type)) {
    node = node.type === "AwaitExpression" ? node.argument : node.expression;
  }
  return node;
}

function propName(node) {
  if (!node) return null;
  if (!node.computed && node.property?.type === "Identifier") {
    return node.property.name;
  }
  if (node.computed && node.property?.type === "Literal") {
    return node.property.value;
  }
  return null;
}

function findVariable(scope, name) {
  for (let s = scope; s; s = s.upper) {
    const v = s.set.get(name);
    if (v) return v;
  }
  return null;
}

// isParsedBody reports whether `node` evaluates to a parsed response body.
function isParsedBody(node, context, seen) {
  node = unwrap(node);
  if (!node) return false;
  switch (node.type) {
    case "CallExpression": {
      const callee = unwrap(node.callee);
      if (callee?.type !== "MemberExpression") return false;
      const name = propName(callee);
      if (name === "json" && node.arguments.length === 0) return true;
      if (
        name === "parse" &&
        callee.object.type === "Identifier" &&
        callee.object.name === "JSON"
      ) {
        return true;
      }
      // `res.json().catch(() => ({}))`
      if (name === "catch") return isParsedBody(callee.object, context, seen);
      return false;
    }
    case "LogicalExpression":
      return (
        isParsedBody(node.left, context, seen) ||
        isParsedBody(node.right, context, seen)
      );
    case "Identifier": {
      if (seen.has(node.name)) return false;
      seen.add(node.name);
      const v = findVariable(context.sourceCode.getScope(node), node.name);
      if (!v) return false;
      for (const def of v.defs) {
        if (
          def.type === "Variable" &&
          def.node.id.type === "Identifier" &&
          isParsedBody(def.node.init, context, seen)
        ) {
          return true;
        }
      }
      for (const ref of v.references) {
        if (ref.writeExpr && isParsedBody(ref.writeExpr, context, seen)) {
          return true;
        }
      }
      return false;
    }
    default:
      return false;
  }
}

function hasErrorMember(typeNode) {
  if (!typeNode) return false;
  if (typeNode.type === "TSTypeLiteral") {
    return typeNode.members.some(
      (m) =>
        m.type === "TSPropertySignature" &&
        ((m.key.type === "Identifier" && m.key.name === "error") ||
          (m.key.type === "Literal" && m.key.value === "error")),
    );
  }
  if (
    typeNode.type === "TSUnionType" ||
    typeNode.type === "TSIntersectionType"
  ) {
    return typeNode.types.some(hasErrorMember);
  }
  return false;
}

function mentionsStatus(node) {
  return node.expressions.some((e) => {
    const x = unwrap(e);
    return x?.type === "MemberExpression" && propName(x) === "status";
  });
}

/** @type {import("eslint").Rule.RuleModule} */
const rule = {
  meta: {
    type: "problem",
    docs: {
      description:
        "Disallow reading a response body's `error` field outside upstreamErrorMessage/upstreamErrorText",
    },
    messages: {
      rawError:
        "Reads a response body's `error` by hand ({{shape}}). Use upstreamErrorMessage(res) or upstreamErrorText(status, body, generic) from lib/api/_shared.ts, so a 5xx body is never shown (#2433).",
    },
    schema: [],
  },
  create(context) {
    const report = (node, shape) =>
      context.report({ node, messageId: "rawError", data: { shape } });
    return {
      MemberExpression(node) {
        if (propName(node) !== "error") return;
        if (isParsedBody(node.object, context, new Set())) {
          report(node, "`.error` on a parsed body");
        }
      },
      VariableDeclarator(node) {
        if (node.id.type !== "ObjectPattern") return;
        const takesError = node.id.properties.some(
          (p) =>
            p.type === "Property" &&
            ((p.key.type === "Identifier" && p.key.name === "error") ||
              (p.key.type === "Literal" && p.key.value === "error")),
        );
        if (takesError && isParsedBody(node.init, context, new Set())) {
          report(node, "`{ error }` destructured from a parsed body");
        }
      },
      TSAsExpression(node) {
        if (hasErrorMember(node.typeAnnotation)) {
          report(node, "cast to `{ error?: … }`");
        }
      },
      TSIntersectionType(node) {
        if (node.parent.type === "TSAsExpression") return; // reported as a cast
        if (
          node.types.some(
            (t) => t.type === "TSTypeLiteral" && hasErrorMember(t),
          )
        ) {
          report(node, "`& { error?: … }`");
        }
      },
      LogicalExpression(node) {
        if (node.operator !== "??") return;
        const left = unwrap(node.left);
        if (left?.type !== "MemberExpression" || propName(left) !== "error") {
          return;
        }
        if (
          node.right.type === "TemplateLiteral" &&
          mentionsStatus(node.right)
        ) {
          report(node, "`.error ?? <status line>`");
        }
      },
    };
  },
};

export default rule;
