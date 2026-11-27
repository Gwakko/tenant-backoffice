// The only place that reads the address of the stack under test. There is no default on purpose:
// a missing value must fail loudly instead of silently testing some other deployment.
export function e2eBaseURL(): string {
  const value = process.env.E2E_BASE_URL;
  if (value === undefined || value.trim() === '') {
    throw new Error(
      'E2E_BASE_URL is not set. Run `make e2e` (stack in containers) or `make e2e-inner` with E2E_BASE_URL pointing at the web service.',
    );
  }
  return value.trim().replace(/\/+$/, '');
}
