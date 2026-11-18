// #2156 fixture — a deliberate route that no help module covers, so the
// help-coverage check must fail on it. Not part of the app:
// `coverage.test.ts` and the route sweep walk this tree.
export default function Page() {
  return null;
}
