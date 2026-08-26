import assert from "node:assert/strict";
import test from "node:test";

import {
	brandingPalette,
	initialView,
	modeLabel,
	needsBrowserHandoff,
	strengthLabel,
	wifiQRPayload,
	type Bootstrap,
} from "./model.ts";

function bootstrap(overrides: Partial<Bootstrap> = {}): Bootstrap {
  return {
		branding: {
			product_name: "Device",
			device_name: "Device",
			title: "Set up your device",
			subtitle: "Choose a connection.",
			primary_color: "#cd2455",
			background_color: "#f8eff3",
		},
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

test("branding palette derives safe supporting colors", () => {
	const palette = brandingPalette(bootstrap().branding);
	assert.equal(palette.accent, "#cd2455");
	assert.equal(palette.accentDark, "#941a3d");
	assert.equal(palette.accentRGB, "205 36 85");
	assert.match(palette.gradient, /^linear-gradient/);

	const fallback = brandingPalette({
		...bootstrap().branding,
		primary_color: "not-a-color",
		background_color: "also-invalid",
	});
	assert.equal(fallback.accent, "#cd2455");
	assert.equal(fallback.backgroundRGB, "248 239 243");
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

test("Wi-Fi QR payload escapes reserved fields", () => {
	assert.equal(
		wifiQRPayload('Display;Wi-Fi', 'pass:word,"\\'),
		'WIFI:T:WPA;S:Display\\;Wi-Fi;P:pass\\:word\\,\\"\\\\;;',
	);
});

test("browser handoff is hidden only on the stable setup origin", () => {
	assert.equal(
		needsBrowserHandoff("http://device.local:18080/networks", "http://device.local:18080/"),
		false,
	);
	assert.equal(
		needsBrowserHandoff("http://10.42.0.1:18080/", "http://device.local:18080/"),
		true,
	);
	assert.equal(needsBrowserHandoff("not a URL", "http://device.local:18080/"), true);
});
