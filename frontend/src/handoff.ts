function escapeWiFiField(value: string): string {
  return value.replace(/[\\;,":]/g, (character) => `\\${character}`);
}

export function wifiQRPayload(ssid: string, password: string): string {
  return `WIFI:T:WPA;S:${escapeWiFiField(ssid)};P:${escapeWiFiField(password)};;`;
}

export function needsBrowserHandoff(currentURL: string, setupURL: string): boolean {
  try {
    return new URL(currentURL).origin !== new URL(setupURL).origin;
  } catch {
    return true;
  }
}
