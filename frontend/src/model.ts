export type Mode = "unknown" | "setup" | "network" | "standalone";
export type OperationKind = "connect" | "standalone";
export type OperationState = "pending" | "running" | "succeeded" | "failed";

export interface Capabilities {
  network: boolean;
  standalone: boolean;
}

export interface Network {
  ssid: string;
  security: "open" | "protected";
  strength: number;
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

export interface Bootstrap {
  capabilities: Capabilities;
  current_mode: Mode;
  operation?: Operation;
  csrf_token: string;
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
