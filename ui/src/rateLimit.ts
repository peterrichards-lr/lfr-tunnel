// Parses what an admin typed as a requests-per-second limit, or null when it is not one.
//
// Shared by every place V2 overrides a live tunnel's rate limit (#2525). parseInt('abc') is NaN,
// which JSON.stringify turns into null, which the handler reads as 0 -- silently REMOVING the
// limit when the intent was to set one. So anything but a whole number >= 0 is refused here.
export function parseRateLimit(input: string): number | null {
  const trimmed = input.trim();
  if (trimmed === '') return null;
  const parsed = Number(trimmed);
  return Number.isInteger(parsed) && parsed >= 0 ? parsed : null;
}
