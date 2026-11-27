// IANA time zone names from the platform (Intl), used by the report form and its URL parameter.

let cache: readonly string[] | undefined;

/** Every zone the platform knows, plus UTC (some engines leave it out of the list). */
export function supportedTimeZones(): readonly string[] {
  if (cache === undefined) {
    const zones = Intl.supportedValuesOf('timeZone');
    cache = zones.includes('UTC') ? zones : ['UTC', ...zones];
  }
  return cache;
}

export function isKnownTimeZone(value: string): boolean {
  return supportedTimeZones().includes(value);
}

/** The zone of the browser when it is a known one, else UTC. */
export function browserTimeZone(): string {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return isKnownTimeZone(zone) ? zone : 'UTC';
}
