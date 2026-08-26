export type Mode = "unknown" | "setup" | "network" | "standalone";
export type OperationKind = "connect" | "standalone";
export type OperationState = "pending" | "running" | "succeeded" | "failed";

export interface Capabilities {
  network: boolean;
  standalone: boolean;
}

export interface Branding {
  product_name: string;
  device_name: string;
  title: string;
  subtitle: string;
  primary_color: string;
  background_color: string;
  logo_url?: string;
}

export interface BrandingPalette {
  accent: string;
  accentDark: string;
  accentPale: string;
  accentWash: string;
  line: string;
  accentRGB: string;
  backgroundRGB: string;
  gradient: string;
}

export interface Network {
  ssid: string;
  security: "open" | "protected";
  strength: number;
}

export interface KnownNetwork {
  uuid: string;
  ssid: string;
  managed_by_onboardd: boolean;
  active: boolean;
  automatic: boolean;
  can_connect: boolean;
  can_forget: boolean;
}

export interface Failure {
  code: string;
  message: string;
}

export interface Operation {
  id: string;
  kind: OperationKind;
  state: OperationState;
  network?: string;
  failure?: Failure;
}

export interface ApplicationHandoff {
  label: string;
  url?: string;
  ready: boolean;
}

export interface Handoff {
  setup_url: string;
  application?: ApplicationHandoff;
  standalone?: StandaloneHandoff;
}

export interface StandaloneHandoff {
  ssid: string;
  password?: string;
}

export interface Bootstrap {
  capabilities: Capabilities;
  current_mode: Mode;
  operation?: Operation;
  handoff?: Handoff;
  csrf_token: string;
}

export function brandingPalette(branding: Branding): BrandingPalette {
  const accent = parseColor(branding.primary_color) ?? [205, 36, 85];
  const background = parseColor(branding.background_color) ?? [248, 239, 243];
  const accentDark = mix(accent, [0, 0, 0], 0.28);
  const accentPale = mix(accent, [255, 255, 255], 0.88);
  const accentWash = mix(accent, [255, 255, 255], 0.96);
  const line = mix(accent, [255, 255, 255], 0.82);
  const gradientStart = mix(accent, [255, 255, 255], 0.22);
  const gradientEnd = mix(accent, [0, 0, 0], 0.22);
  return {
    accent: toHex(accent),
    accentDark: toHex(accentDark),
    accentPale: toHex(accentPale),
    accentWash: toHex(accentWash),
    line: toHex(line),
    accentRGB: accent.join(" "),
    backgroundRGB: background.join(" "),
    gradient: `linear-gradient(135deg, ${toHex(gradientStart)} 0%, ${toHex(accent)} 52%, ${toHex(gradientEnd)} 100%)`,
  };
}

function parseColor(value: string): [number, number, number] | undefined {
  if (!/^#[0-9a-fA-F]{6}$/.test(value)) return undefined;
  return [
    Number.parseInt(value.slice(1, 3), 16),
    Number.parseInt(value.slice(3, 5), 16),
    Number.parseInt(value.slice(5, 7), 16),
  ];
}

function mix(
  color: [number, number, number],
  target: [number, number, number],
  amount: number,
): [number, number, number] {
  return color.map((channel, index) =>
    Math.round(channel + (target[index]! - channel) * amount),
  ) as [number, number, number];
}

function toHex(color: [number, number, number]): string {
  return `#${color.map((channel) => channel.toString(16).padStart(2, "0")).join("")}`;
}

export type InitialView = "operation" | "choose-mode" | "networks" | "standalone";

export function initialView(bootstrap: Bootstrap): InitialView {
  if (bootstrap.operation) {
    return "operation";
  }
  if (bootstrap.capabilities.network && bootstrap.capabilities.standalone) {
    return "choose-mode";
  }
  if (bootstrap.capabilities.network) {
    return "networks";
  }
  return "standalone";
}

export function strengthLabel(strength: number): string {
  if (strength >= 75) return "Strong signal";
  if (strength >= 45) return "Good signal";
  return "Weak signal";
}

export function modeLabel(mode: Mode): string {
  switch (mode) {
    case "network":
      return "Connected to Wi-Fi";
    case "standalone":
      return "Using standalone mode";
    case "setup":
      return "Setup mode";
    default:
      return "Not configured";
  }
}

export function wifiQRPayload(ssid: string, password: string): string {
  const escapeField = (value: string): string =>
    value.replace(/[\\;,":]/g, (character) => `\\${character}`);
  return `WIFI:T:WPA;S:${escapeField(ssid)};P:${escapeField(password)};;`;
}

export function needsBrowserHandoff(currentURL: string, setupURL: string): boolean {
  try {
    return new URL(currentURL).origin !== new URL(setupURL).origin;
  } catch {
    return true;
  }
}
