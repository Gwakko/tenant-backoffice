import { networkProblem, parseProblem, type ApiProblem } from './problem';

// A failed request as an exception, so that TanStack Query can carry it in `error`.
export class ApiError extends Error {
  readonly problem: ApiProblem;

  constructor(problem: ApiProblem) {
    super(problem.code ?? `http_${String(problem.status)}`);
    this.name = 'ApiError';
    this.problem = problem;
  }
}

export function problemOf(error: unknown): ApiProblem {
  return error instanceof ApiError ? error.problem : networkProblem();
}

interface Settled<D> {
  data?: D;
  error?: unknown;
  response: Response;
}

export interface Unwrapped<D> {
  data: D;
  response: Response;
}

/** Turns an openapi-fetch answer into data, or throws ApiError (also for a transport failure). */
export async function unwrap<D>(request: Promise<Settled<D>>): Promise<Unwrapped<D>> {
  let settled: Settled<D>;
  try {
    settled = await request;
  } catch {
    throw new ApiError(networkProblem());
  }
  if (settled.data === undefined) throw new ApiError(parseProblem(settled.response, settled.error));
  return { data: settled.data, response: settled.response };
}

/** The same for answers without a body (204). */
export async function unwrapEmpty(
  request: Promise<{ data?: undefined; error?: unknown; response: Response }>,
): Promise<Response> {
  let settled: { data?: undefined; error?: unknown; response: Response };
  try {
    settled = await request;
  } catch {
    throw new ApiError(networkProblem());
  }
  if (!settled.response.ok) throw new ApiError(parseProblem(settled.response, settled.error));
  return settled.response;
}
