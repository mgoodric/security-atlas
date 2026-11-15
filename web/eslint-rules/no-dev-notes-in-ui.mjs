// #2082 — fail on developer notes leaking into user-visible UI text.
//
// The UI audit (#2070) found slice numbers, canvas section references and
// API paths in copy across a dozen pages. They mean nothing to the person
// using the app and say more about how it was built than what it does.
//
// Checked: JSX text, string literals rendered as JSX children, and the
// user-visible string props below. Code that is not rendered (a fetch URL,
// a comment, a key) is out of scope. Tests are excluded in
// eslint.config.mjs; there is no allow-list.

const PATTERNS = [
  { re: /slice\s*\d/i, label: "a slice number" },
  { re: /canvas\s*§/i, label: "a canvas section" },
  { re: /\/v1\//, label: "an API path" },
];

export const VISIBLE_PROPS = new Set([
  "title",
  "description",
  "placeholder",
  "aria-label",
  "alt",
]);

function match(text) {
  if (typeof text !== "string") return null;
  return PATTERNS.find((p) => p.re.test(text)) ?? null;
}

/** @type {import("eslint").Rule.RuleModule} */
const rule = {
  meta: {
    type: "problem",
    docs: {
      description:
        "Disallow slice numbers, canvas sections and API paths in user-visible UI text",
    },
    messages: {
      devNote:
        "User-visible text mentions {{what}}. Say what the user can do or what is missing instead.",
    },
    schema: [],
  },
  create(context) {
    function report(node, text) {
      const hit = match(text);
      if (hit) {
        context.report({
          node,
          messageId: "devNote",
          data: { what: hit.label },
        });
      }
    }

    // Walk the parts of an expression that end up rendered as text.
    function checkExpression(node) {
      if (!node) return;
      switch (node.type) {
        case "Literal":
          report(node, node.value);
          break;
        case "TemplateLiteral":
          for (const q of node.quasis) report(q, q.value.cooked);
          break;
        case "ConditionalExpression":
          checkExpression(node.consequent);
          checkExpression(node.alternate);
          break;
        case "LogicalExpression":
          checkExpression(node.right);
          break;
        case "BinaryExpression":
          if (node.operator === "+") {
            checkExpression(node.left);
            checkExpression(node.right);
          }
          break;
      }
    }

    return {
      JSXText(node) {
        report(node, node.value);
      },
      JSXExpressionContainer(node) {
        // Children only; attribute values are handled below.
        if (node.parent?.type === "JSXAttribute") return;
        checkExpression(node.expression);
      },
      JSXAttribute(node) {
        if (node.name.type !== "JSXIdentifier") return;
        if (!VISIBLE_PROPS.has(node.name.name)) return;
        const v = node.value;
        if (!v) return;
        if (v.type === "Literal") report(v, v.value);
        else if (v.type === "JSXExpressionContainer")
          checkExpression(v.expression);
      },
    };
  },
};

export default rule;
