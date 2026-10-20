import { beforeEach, describe, expect, test } from 'bun:test';
import { invalidateAuthFileDerivedCaches } from '../src/features/authFiles/cacheInvalidation';
import {
  captureQuotaCacheGeneration,
  commitIfQuotaCacheCurrent,
  useQuotaStore,
} from '../src/stores/useQuotaStore';

describe('quota cache session isolation', () => {
  beforeEach(() => {
    useQuotaStore.getState().clearQuotaCache();
  });

  test('prevents an earlier connection from committing after the cache is cleared', () => {
    const previousConnection = captureQuotaCacheGeneration();
    let committed = false;

    useQuotaStore.getState().clearQuotaCache();

    expect(
      commitIfQuotaCacheCurrent(previousConnection, () => {
        committed = true;
      })
    ).toBe(false);
    expect(committed).toBe(false);
  });

  test('allows the current connection generation to commit', () => {
    const currentConnection = captureQuotaCacheGeneration();
    let committed = false;

    expect(
      commitIfQuotaCacheCurrent(currentConnection, () => {
        committed = true;
      })
    ).toBe(true);
    expect(committed).toBe(true);
  });

  test('single-file invalidation preserves other credentials and their pending requests', () => {
    const target = { status: 'error' as const, error: 'HTTP 500' };
    const other = { status: 'loading' as const };
    useQuotaStore.getState().setQuotaPolls({ 'a.json': target, 'b.json': other });
    useQuotaStore.getState().setKimiQuota({ 'c.json': { status: 'loading', rows: [] } });
    const kimiCache = useQuotaStore.getState().kimiQuota;
    const targetRequest = captureQuotaCacheGeneration('a.json');
    const otherRequest = captureQuotaCacheGeneration('b.json');
    const batchRequest = captureQuotaCacheGeneration();

    invalidateAuthFileDerivedCaches(() => {}, ['a.json']);

    expect(useQuotaStore.getState().quotaPolls).toEqual({ 'b.json': other });
    expect(useQuotaStore.getState().quotaPolls['b.json']).toBe(other);
    expect(useQuotaStore.getState().kimiQuota).toBe(kimiCache);
    expect(commitIfQuotaCacheCurrent(targetRequest, () => {})).toBe(false);
    expect(commitIfQuotaCacheCurrent(otherRequest, () => {})).toBe(true);
    expect(commitIfQuotaCacheCurrent(batchRequest, () => {}, 'a.json')).toBe(false);
    expect(commitIfQuotaCacheCurrent(batchRequest, () => {}, 'b.json')).toBe(true);
    expect(commitIfQuotaCacheCurrent(captureQuotaCacheGeneration('a.json'), () => {})).toBe(true);

    useQuotaStore.getState().clearQuotaCache();
    expect(commitIfQuotaCacheCurrent(otherRequest, () => {})).toBe(false);
    expect(commitIfQuotaCacheCurrent(batchRequest, () => {}, 'b.json')).toBe(false);
  });

  test('empty names do nothing; omitted names still invalidate all credentials', () => {
    useQuotaStore.getState().setQuotaPolls({ 'a.json': { status: 'loading' } });
    const request = captureQuotaCacheGeneration('a.json');
    const previous = useQuotaStore.getState();
    invalidateAuthFileDerivedCaches(() => {}, []);
    expect(useQuotaStore.getState()).toBe(previous);
    invalidateAuthFileDerivedCaches(() => {});
    expect(useQuotaStore.getState().quotaPolls).toEqual({});
    expect(commitIfQuotaCacheCurrent(request, () => {})).toBe(false);
  });

  test('clears same-name quota and rejects an in-flight commit after auth mutation', () => {
    const fileName = 'shared-codex.json';
    useQuotaStore.getState().setQuotaPolls({ [fileName]: { status: 'error', error: 'account-a' } });
    const accountARequest = captureQuotaCacheGeneration(fileName);
    let invalidatedNames: string[] | undefined;

    invalidateAuthFileDerivedCaches(
      (names) => {
        invalidatedNames = names;
      },
      [fileName]
    );

    expect(invalidatedNames).toEqual([fileName]);
    expect(useQuotaStore.getState().quotaPolls[fileName]).toBeUndefined();

    let committed = false;
    expect(
      commitIfQuotaCacheCurrent(accountARequest, () => {
        committed = true;
      })
    ).toBe(false);
    expect(committed).toBe(false);
  });
});
