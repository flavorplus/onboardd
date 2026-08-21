import assert from "node:assert/strict";
import test from "node:test";

import { initialView, modeLabel, strengthLabel, type Bootstrap } from "./model.ts";

function bootstrap(overrides: Partial<Bootstrap> = {}): Bootstrap {
  return {
    capabilities: { network: true, standalone: true },
    current_mode: "setup",
    csrf_token: "token",
    ...overrides,
  };
}

test("active operations take precedence over mode choice", () => {
  assert.equal(
    initialView(
      bootstrap({
        operation: { id: "1", kind: "connect", state: "running" },
      }),
    ),
    "operation",
  );
});

test("completed operations survive a browser reconnect", () => {
  const state = bootstrap();
  state.operation = {
    id: "finished-operation",
    kind: "connect",
    state: "succeeded",
    network: "Office",
  };
  assert.equal(initialView(state), "operation");
});

test("single allowed modes skip the choice screen", () => {
  assert.equal(
    initialView(bootstrap({ capabilities: { network: true, standalone: false } })),
    "networks",
  );
  assert.equal(
    initialView(bootstrap({ capabilities: { network: false, standalone: true } })),
    "standalone",
  );
});

test("signal and mode labels stay product-facing", () => {
  assert.equal(strengthLabel(80), "Strong signal");
  assert.equal(strengthLabel(50), "Good signal");
  assert.equal(strengthLabel(20), "Weak signal");
  assert.equal(modeLabel("network"), "Connected to Wi-Fi");
  assert.equal(modeLabel("standalone"), "Using standalone mode");
});
