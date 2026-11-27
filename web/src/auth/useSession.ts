import { useSyncExternalStore } from 'react';

import { getSnapshot, subscribe, type SessionSnapshot } from './session';

export function useSession(): SessionSnapshot {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
