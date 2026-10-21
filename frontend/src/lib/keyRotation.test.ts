import { test } from "node:test";
import assert from "node:assert/strict";
import { rotationStatus, rotationReminder } from "./keyRotation.ts";

test("rotation date boundaries include today and exactly 30 days", () => {
  const now = new Date(2026, 9, 21, 23, 59);
  assert.equal(rotationStatus("2026-10-20", now), "overdue");
  assert.equal(rotationStatus("2026-10-21", now), "soon");
  assert.equal(rotationStatus("2026-11-20", now), "soon");
  assert.equal(rotationStatus("2026-11-21", now), "scheduled");
  for (const date of [undefined, "", "2026-02-29", "0000-01-01", "2026-1-01", "2026-10-21T00:00:00Z"]) {
    assert.equal(rotationStatus(date, now), "none", date);
  }
  assert.equal(rotationStatus("2024-02-29", now), "overdue");
});

test("rotation uses local calendar days across DST and year rollover", () => {
  const originalTZ = process.env.TZ;
  try {
    for (const tz of ["America/New_York", "Pacific/Auckland", "Pacific/Honolulu"]) {
      process.env.TZ = tz;
      const now = new Date(2026, 9, 21, 0, 1);
      assert.equal(rotationStatus("2026-10-21", now), "soon", tz);
      assert.equal(rotationStatus("2026-11-20", now), "soon", tz);
      assert.equal(rotationStatus("2026-11-21", now), "scheduled", tz);
      assert.equal(rotationStatus("2027-01-01", new Date(2026, 11, 31, 23, 59)), "soon", tz);
    }
  } finally {
    if (originalTZ === undefined) delete process.env.TZ;
    else process.env.TZ = originalTZ;
  }
});

test("visit reminder aggregates hardware and software dates without key details", () => {
  const now = new Date(2026, 9, 21);
  const keys = [
    { rotationDueDate: "2026-10-20", hardware: false, name: "secret-name", publicKey: "key-material", fingerprint: "fingerprint" },
    { rotationDueDate: "2026-10-21", hardware: true, name: "token-name" },
    { rotationDueDate: "2026-12-01" },
    { rotationDueDate: "" },
  ];
  assert.equal(rotationReminder(keys, now), "1 overdue · 1 due within 30 days. Review rotation dates in Keys. Reminders are advisory; authentication is unchanged.");
  assert.equal(rotationReminder([], now), null);
  assert.equal(rotationReminder([{ rotationDueDate: "" }, { rotationDueDate: "2026-12-01" }], now), null);
});
