export type FrontendDesign = "midnight" | "newsroom" | "healthmap";
export type ThemeMode = "light" | "dark";

export const DESIGN_NAMES: FrontendDesign[] = ["midnight", "newsroom", "healthmap"];

export function isValidDesign(value: string): value is FrontendDesign {
  return DESIGN_NAMES.includes(value as FrontendDesign);
}
