import { randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import path from 'node:path';

import { expect, test as base, type APIRequestContext, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { z } from 'zod';

import { e2eBaseURL } from './env';

// ---------------------------------------------------------------------------
// Demo data: logins and passwords are read from the seed file, never copied into the tests.
// ---------------------------------------------------------------------------

const demoSchema = z.object({
  nodes: z.array(z.object({ id: z.string(), type: z.string(), name: z.string() })),
  users: z.array(z.object({ id: z.string(), email: z.string(), password: z.string() })),
});

const demo = demoSchema.parse(
  JSON.parse(readFileSync(path.resolve(import.meta.dirname, '../../backend/seed/demo.json'), 'utf8')),
);

export interface Account {
  email: string;
  password: string;
}

export interface NodeRef {
  id: string;
  name: string;
}

/** A demo account by the local part of its e-mail, for example `admin.alpha`. */
export function seedAccount(localPart: string): Account {
  const found = demo.users.find((user) => user.email.startsWith(`${localPart}@`));
  if (!found) throw new Error(`no demo user "${localPart}" in backend/seed/demo.json`);
  return { email: found.email, password: found.password };
}

/** A demo node by its exact name. */
export function seedNode(name: string): NodeRef {
  const found = demo.nodes.find((node) => node.name === name);
  if (!found) throw new Error(`no demo node "${name}" in backend/seed/demo.json`);
  return { id: found.id, name: found.name };
}

// ---------------------------------------------------------------------------
// API helper: used only to prepare and to inspect state, never for the steps a scenario is about.
// The contexts are created here, not by the `request` fixture, so they do not enter traces.
// ---------------------------------------------------------------------------

const API = '/api/v1';
const TOKEN_LIFETIME_MS = 8 * 60_000;

const tokenSchema = z.object({ accessToken: z.string() });
const nodeSchema = z.object({ id: z.string(), name: z.string() });
const balanceSchema = z.object({ amount: z.string() });

export class Api {
  private token = '';
  private tokenAt = 0;

  constructor(
    private readonly http: APIRequestContext,
    private readonly account: Account,
  ) {}

  private async bearer(): Promise<Record<string, string>> {
    if (this.token === '' || Date.now() - this.tokenAt > TOKEN_LIFETIME_MS) {
      const response = await this.http.post(`${API}/auth/login`, { data: this.account });
      // Only the status is reported: neither the body nor the token may reach a log.
      expect(response.status(), 'API login').toBe(200);
      this.token = tokenSchema.parse(await response.json()).accessToken;
      this.tokenAt = Date.now();
    }
    return { Authorization: `Bearer ${this.token}` };
  }

  async createNode(parentId: string, type: 'partner' | 'company' | 'unit', name: string): Promise<NodeRef> {
    const response = await this.http.post(`${API}/nodes/${parentId}/children`, {
      headers: await this.bearer(),
      data: { type, name },
    });
    expect(response.status(), `create ${type} "${name}"`).toBe(201);
    return nodeSchema.parse(await response.json());
  }

  async addMember(nodeId: string, account: Account, role: 'admin' | 'viewer'): Promise<void> {
    const response = await this.http.post(`${API}/nodes/${nodeId}/members`, {
      headers: await this.bearer(),
      data: { email: account.email, role, initialPassword: account.password },
    });
    expect(response.status(), `grant ${role} to ${account.email}`).toBe(201);
  }

  /** The balance in minor units, as the API reports it. */
  async balance(nodeId: string): Promise<string> {
    const response = await this.http.get(`${API}/nodes/${nodeId}/balance`, { headers: await this.bearer() });
    expect(response.status(), 'read balance').toBe(200);
    return balanceSchema.parse(await response.json()).amount;
  }
}

// ---------------------------------------------------------------------------
// Fresh data per scenario: scenarios never share nodes or users and never change the demo tree.
// ---------------------------------------------------------------------------

export interface Provisioned {
  /** A new node under the given demo node. */
  node: NodeRef;
  /** A new account holding `role` on that node only. */
  account: Account;
}

export type Provisioner = (parent: NodeRef, type: 'company' | 'unit', role: 'admin' | 'viewer') => Promise<Provisioned>;

function suffix(): string {
  return randomBytes(4).toString('hex');
}

// ---------------------------------------------------------------------------
// UI helpers
// ---------------------------------------------------------------------------

/** Signs in through the login form and waits for the start page. */
export async function signIn(page: Page, account: Account): Promise<void> {
  await page.goto('/login');
  await page.getByLabel('Email').fill(account.email);
  await page.getByLabel('Password').fill(account.password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByText(`Signed in as ${account.email}.`)).toBeVisible();
}

/** Signs in as a demo user from the seed file. */
export async function signInAs(page: Page, localPart: string): Promise<Account> {
  const account = seedAccount(localPart);
  await signIn(page, account);
  return account;
}

export interface Session {
  context: BrowserContext;
  page: Page;
}

interface TestFixtures {
  provision: Provisioner;
  /** A second, fully isolated browser context (own cookies and storage), closed after the test. */
  openSession: () => Promise<Session>;
}

interface WorkerFixtures {
  /** The platform owner of the demo data, used to prepare state through the API. */
  owner: Api;
}

export const test = base.extend<TestFixtures, WorkerFixtures>({
  owner: [
    async ({ playwright }, give) => {
      const http = await playwright.request.newContext({ baseURL: e2eBaseURL() });
      await give(new Api(http, seedAccount('owner')));
      await http.dispose();
    },
    { scope: 'worker' },
  ],

  provision: async ({ owner }, give) => {
    const make: Provisioner = async (parent, type, role) => {
      const id = suffix();
      const node = await owner.createNode(parent.id, type, `E2E ${type} ${id}`);
      const account: Account = { email: `e2e.${id}@demo.test`, password: `e2e-${randomBytes(9).toString('hex')}` };
      await owner.addMember(node.id, account, role);
      return { node, account };
    };
    await give(make);
  },

  openSession: async ({ browser, baseURL }, give) => {
    const opened: BrowserContext[] = [];
    await give(async () => {
      const context = await newIsolatedContext(browser, baseURL);
      opened.push(context);
      return { context, page: await context.newPage() };
    });
    for (const context of opened) await context.close();
  },
});

function newIsolatedContext(browser: Browser, baseURL: string | undefined): Promise<BrowserContext> {
  return browser.newContext(baseURL === undefined ? {} : { baseURL });
}

export { expect };
