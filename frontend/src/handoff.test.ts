import assert from "node:assert/strict";
import test from "node:test";

import { needsBrowserHandoff, wifiQRPayload } from "./handoff.ts";

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
