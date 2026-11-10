// Shared API loaders that populate the console store.
import { api } from "./api.js";
import * as store from "./store.js";
import { toast } from "./ui.js";

// The Usage tab window the user picks with the range slider. It lives in
// localStorage so the choice survives a reload, and the API clamps it to
// [1, 24] hours.
const USAGE_HOURS_KEY = "opencode2api.ui.usageHours";

export const USAGE_HOURS = { min: 1, max: 24, step: 1, initial: 12 };

export function getUsageHours() {
  try {
    const value = Number(localStorage.getItem(USAGE_HOURS_KEY));
    return Number.isInteger(value) && value >= USAGE_HOURS.min && value <= USAGE_HOURS.max
      ? value
      : USAGE_HOURS.initial;
  } catch {
    return USAGE_HOURS.initial;
  }
}

export function setUsageHours(hours) {
  const value = Math.min(
    USAGE_HOURS.max,
    Math.max(USAGE_HOURS.min, Math.round(Number(hours) || USAGE_HOURS.initial)),
  );
  try {
    localStorage.setItem(USAGE_HOURS_KEY, String(value));
  } catch {
    // A blocked localStorage only loses the remembered selection.
  }
  return value;
}

export async function refreshMonitor() {
  try {
    store.set("monitor", await api(`/api/monitor?usage_hours=${getUsageHours()}`));
  } catch {
    // The 3s poll stays silent; transient failures surface in the log stream.
  }
}

export async function loadConfig() {
  store.set("config", await api("/api/config"));
}

export async function loadDebugModels() {
  try {
    const data = await api("/api/debug/models");
    if (data.last_inference) store.set("lastPlayground", data.last_inference);
    store.set("debug", data);
  } catch (error) {
    toast(error.message, true);
  }
}
