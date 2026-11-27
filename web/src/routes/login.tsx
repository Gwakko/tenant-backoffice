import { zodResolver } from '@hookform/resolvers/zod';
import { createFileRoute, redirect, useRouter } from '@tanstack/react-router';
import { useRef, useState } from 'react';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import { describeProblem, type ApiProblem } from '@/api/problem';
import { safeRedirect } from '@/auth/redirect';
import { login } from '@/auth/service';
import { hasSession } from '@/auth/session';

export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === 'string' ? { redirect: search.redirect } : {},
  beforeLoad: ({ search }) => {
    if (hasSession()) throw redirect({ href: safeRedirect(search.redirect) });
  },
  component: LoginPage,
});

const loginSchema = z.object({
  email: z.string().trim().min(1, 'Enter your email.').max(254, 'The email is too long.').pipe(z.email('Enter a valid email.')),
  password: z.string().min(1, 'Enter your password.').max(128, 'The password is too long.'),
});

type LoginForm = z.infer<typeof loginSchema>;

function LoginPage() {
  const { api } = Route.useRouteContext();
  const search = Route.useSearch();
  const router = useRouter();
  const [problem, setProblem] = useState<ApiProblem | null>(null);
  const inFlight = useRef(false);
  const {
    register,
    handleSubmit,
    setValue,
    formState: { errors, isSubmitting },
  } = useForm<LoginForm>({
    resolver: zodResolver(loginSchema),
    defaultValues: { email: '', password: '' },
  });

  const onSubmit = async (values: LoginForm) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setProblem(null);
    try {
      const result = await login(api, values);
      // The password does not outlive the request, whatever the outcome.
      setValue('password', '');
      if (result.ok) {
        router.history.replace(safeRedirect(search.redirect));
      } else {
        setProblem(result.problem);
      }
    } finally {
      inFlight.current = false;
    }
  };

  return (
    <section className="mx-auto max-w-sm">
      <h1 className="text-xl font-semibold">Sign in</h1>
      <form noValidate onSubmit={(event) => void handleSubmit(onSubmit)(event)} className="mt-4 space-y-4" aria-busy={isSubmitting}>
        {problem && (
          <div role="alert" className="rounded border border-red-300 bg-red-50 px-3 py-2 text-sm text-red-800">
            {describeProblem(problem)}
          </div>
        )}
        <div>
          <label htmlFor="email" className="block text-sm font-medium">
            Email
          </label>
          <input
            id="email"
            type="email"
            autoComplete="username"
            aria-invalid={errors.email ? true : undefined}
            aria-describedby={errors.email ? 'email-error' : undefined}
            className="mt-1 w-full rounded border border-slate-300 px-3 py-2"
            {...register('email')}
          />
          {errors.email && (
            <p id="email-error" className="mt-1 text-sm text-red-700">
              {errors.email.message}
            </p>
          )}
        </div>
        <div>
          <label htmlFor="password" className="block text-sm font-medium">
            Password
          </label>
          <input
            id="password"
            type="password"
            autoComplete="current-password"
            aria-invalid={errors.password ? true : undefined}
            aria-describedby={errors.password ? 'password-error' : undefined}
            className="mt-1 w-full rounded border border-slate-300 px-3 py-2"
            {...register('password')}
          />
          {errors.password && (
            <p id="password-error" className="mt-1 text-sm text-red-700">
              {errors.password.message}
            </p>
          )}
        </div>
        <button
          type="submit"
          disabled={isSubmitting}
          className="w-full rounded bg-slate-900 px-3 py-2 text-white hover:bg-slate-800 disabled:opacity-50"
        >
          {isSubmitting ? 'Signing in...' : 'Sign in'}
        </button>
      </form>
    </section>
  );
}
