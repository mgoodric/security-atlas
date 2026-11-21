// TEMPORARY (#2793 proof, reverted before merge): shift the Playwright
// runner's clock so it reads 00:01 UTC today. The browser clock is set by
// each spec; the bearer is minted by the server, so only the runner moves.
const RealDate = Date;
const n = new RealDate();
const target = RealDate.UTC(
  n.getUTCFullYear(),
  n.getUTCMonth(),
  n.getUTCDate(),
  0,
  1,
  0,
);
const offset = target - RealDate.now();
class FakeDate extends RealDate {
  constructor(...a) {
    if (a.length === 0) super(RealDate.now() + offset);
    else super(...a);
  }
  static now() {
    return RealDate.now() + offset;
  }
}
globalThis.Date = FakeDate;
