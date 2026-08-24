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
  assert.equal(
    record(record(setup.body).handoff).standalone !== undefined,
    true,
  );

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

test("simulates configured branding and an optional logo", () => {
  const device = new MockDevice("both", "custom");
  const setup = record(device.handle({ method: "GET", path: "/api/v1/setup" }, 1000).body);
  const branding = record(setup.branding);
  const handoff = record(setup.handoff);
  assert.equal(branding.product_name, "InkyPi");
  assert.equal(branding.logo_url, "/api/v1/branding/logo");
  assert.equal(handoff.setup_url, "http://127.0.0.1:5173/");
});

test("gates the application destination until it becomes ready", () => {
  const device = new MockDevice();
  const accepted = device.handle(
    {
      method: "POST",
      path: "/api/v1/standalone",
      csrfToken: mockCSRFToken,
      body: { confirm: true },
    },
    1000,
  );
  assert.equal(accepted.status, 202);

  const starting = record(device.handle({ method: "GET", path: "/api/v1/setup" }, 4000).body);
  const startingApplication = record(record(starting.handoff).application);
  assert.equal(startingApplication.ready, false);
  assert.equal(startingApplication.url, undefined);

  const ready = record(device.handle({ method: "GET", path: "/api/v1/setup" }, 7000).body);
  const readyApplication = record(record(ready.handoff).application);
  assert.equal(readyApplication.ready, true);
  assert.equal(readyApplication.url, "http://device.local/");
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
