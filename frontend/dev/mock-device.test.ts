import assert from "node:assert/strict";
import test from "node:test";

import { MockDevice, mockCSRFToken, rejectedMockPassword } from "./mock-device.ts";

test("simulates a disconnect followed by a rejected connection", () => {
  const device = new MockDevice("both");
  const accepted = device.handle(
    {
      method: "POST",
      path: "/api/v1/connections",
      csrfToken: mockCSRFToken,
      body: { ssid: "Studio Wi-Fi", password: rejectedMockPassword },
    },
    1000,
  );
  assert.equal(accepted.status, 202);
  const id = operationID(accepted.body);

  assert.equal(device.handle({ method: "GET", path: `/api/v1/operations/${id}` }, 2000).status, 503);
  const failed = device.handle({ method: "GET", path: `/api/v1/operations/${id}` }, 4000);
  assert.equal(operationState(failed.body), "failed");
});

test("simulates successful standalone selection and capability policy", () => {
  const device = new MockDevice("standalone");
  const setup = device.handle({ method: "GET", path: "/api/v1/setup" }, 1000);
  assert.deepEqual(capabilities(setup.body), { network: false, standalone: true });

  const accepted = device.handle(
    {
      method: "POST",
      path: "/api/v1/standalone",
      csrfToken: mockCSRFToken,
      body: { confirm: true },
    },
    1000,
  );
  const id = operationID(accepted.body);
  device.handle({ method: "GET", path: `/api/v1/operations/${id}` }, 2000);
  const completed = device.handle({ method: "GET", path: `/api/v1/operations/${id}` }, 4000);
  assert.equal(operationState(completed.body), "succeeded");

  const refreshed = device.handle({ method: "GET", path: "/api/v1/setup" }, 4000);
  assert.equal(currentMode(refreshed.body), "standalone");
});

function record(value: unknown): Record<string, unknown> {
  assert.equal(typeof value, "object");
  assert.notEqual(value, null);
  return value as Record<string, unknown>;
}

function operationID(body: unknown): string {
  return String(record(record(body).operation).id);
}

function operationState(body: unknown): string {
  return String(record(record(body).operation).state);
}

function capabilities(body: unknown): unknown {
  return record(body).capabilities;
}

function currentMode(body: unknown): string {
  return String(record(body).current_mode);
}
