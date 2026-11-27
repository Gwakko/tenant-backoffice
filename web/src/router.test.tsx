import { QueryClient } from '@tanstack/react-query';
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { createAppRouter } from '@/router';

describe('routing', () => {
  it('renders the root layout and the index route', async () => {
    const router = createAppRouter(new QueryClient(), createMemoryHistory({ initialEntries: ['/'] }));

    render(<RouterProvider router={router} />);

    expect(await screen.findByRole('heading', { name: 'Tenant Backoffice' })).toBeTruthy();
    expect(screen.getAllByText('Tenant Backoffice').length).toBeGreaterThan(1);
  });
});
