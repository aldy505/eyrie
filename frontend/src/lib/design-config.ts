import type { Metadata, RegionData } from "@/lib/status-dashboard";
import {
  getAvailabilityStatus,
  type AvailabilityStatus,
} from "@/lib/status-dashboard";

export type FrontendDesign = "midnight" | "dithered" | "newsroom" | "healthmap" | "neobrutalism" | "hearth";
export type ThemeMode = "light" | "dark";

export const DESIGN_NAMES: FrontendDesign[] = ["midnight", "dithered", "newsroom", "healthmap", "neobrutalism", "hearth"];

export function isValidDesign(value: string): value is FrontendDesign {
  return DESIGN_NAMES.includes(value as FrontendDesign);
}

export function getTodayDowntimeMinutes(region: RegionData["monitors"][number]): number {
  return region.downtimes["0"]?.duration_minutes ?? 0;
}

export function getRegionStatus(
  region: RegionData["monitors"][number],
  metadata: Metadata,
): AvailabilityStatus {
  const downtime = getTodayDowntimeMinutes(region);
  return getAvailabilityStatus(downtime, metadata);
}

export function getResponseTimeTone(ms: number): "fast" | "normal" | "slow" | "critical" {
  if (ms < 100) return "fast";
  if (ms < 500) return "normal";
  if (ms <= 2000) return "slow";
  return "critical";
}
