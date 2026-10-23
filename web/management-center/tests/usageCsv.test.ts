import { describe, expect, test } from 'bun:test';
import { buildUsageCsv, escapeCsvCell, usageCsvFilename } from '@/features/usage/usageCsv';
import { buildClientLabels } from '@/features/requests/requestsModel';
import { normalizeUsageReport } from '@/services/api/usageHistory';
import { rawFoldedReport, rawReport } from './fixtures/usageHistory';

const HEADER_TAIL =
  'requests,failures,avg_latency_ms,uncached_tokens,cache_read_tokens,cache_write_5m_tokens,' +
  'cache_write_1h_tokens,output_tokens,reasoning_tokens,unclassified_tokens,total_tokens,' +
  'cost_usd,unpriced_tokens';

const lines = (csv: string) => csv.slice(1).split('\r\n');

describe('escapeCsvCell', () => {
  test('quotes commas, quotes and line breaks per RFC 4180', () => {
    expect(escapeCsvCell('plain')).toBe('plain');
    expect(escapeCsvCell('a,b')).toBe('"a,b"');
    expect(escapeCsvCell('say "hi"')).toBe('"say ""hi"""');
    expect(escapeCsvCell('two\nlines')).toBe('"two\nlines"');
  });

  test('guards cells a spreadsheet would run as a formula', () => {
    expect(escapeCsvCell('=HYPERLINK("http://x")')).toBe(`"'=HYPERLINK(""http://x"")"`);
    expect(escapeCsvCell('+1')).toBe("'+1");
    expect(escapeCsvCell('-2')).toBe("'-2");
    expect(escapeCsvCell('@SUM(A1)')).toBe("'@SUM(A1)");
    expect(escapeCsvCell('\tTab')).toBe("'\tTab");
    expect(escapeCsvCell('\rx')).toBe(`"'\rx"`);
  });
});

describe('buildUsageCsv', () => {
  const report = normalizeUsageReport(rawReport());

  test('starts with a BOM, uses CRLF and one row per bucket and group with traffic', () => {
    const csv = buildUsageCsv(report, { dimension: 'model', label: (key) => key });
    expect(csv.startsWith('\uFEFF')).toBe(true);
    expect(csv.endsWith('\r\n')).toBe(true);
    const rows = lines(csv).filter(Boolean);
    expect(rows[0]).toBe(`bucket_start,bucket_end,model,model_label,${HEADER_TAIL}`);
    expect(rows).toHaveLength(1 + 4);
    expect(rows[1]).toBe(
      '2026-10-20T00:00:00-04:00,2026-10-21T00:00:00-04:00,claude-opus-5-5,claude-opus-5-5,' +
        '6,1,1521,1200,80000,3000,1000,900,100,0,86200,0.060000,0'
    );
  });

  test('an unpriced cell has an empty cost, not zero', () => {
    const csv = buildUsageCsv(report, { dimension: 'model', label: (key) => key });
    const devin = lines(csv).find((row) => row.includes('devin/swe-1'));
    expect(devin?.split(',').slice(-2)).toEqual(['', '85200']);
  });

  test('rows follow the report group order inside each bucket', () => {
    const csv = buildUsageCsv(report, { dimension: 'model', label: (key) => key });
    const models = lines(csv)
      .slice(1)
      .filter(Boolean)
      .map((row) => row.split(',')[2]);
    expect(models).toEqual([
      'claude-opus-5-5',
      'claude-sonnet-5',
      'claude-opus-5-5',
      'devin/swe-1',
    ]);
  });

  test('group by none drops the dimension columns', () => {
    const none = normalizeUsageReport(
      rawReport({
        query: { from: '2026-10-20', to: '2026-10-22', tz: 'UTC', bucket: 'day', group_by: 'none' },
      })
    );
    const csv = buildUsageCsv(none, { dimension: 'none', label: (key) => key });
    expect(lines(csv)[0]).toBe(`bucket_start,bucket_end,${HEADER_TAIL}`);
  });

  test('client keys export as hash and label, never the raw key', () => {
    const folded = normalizeUsageReport(rawFoldedReport());
    const labels = buildClientLabels({
      hashes: folded.options.key,
      keys: folded.keys,
      apiKeys: ['sk-live-secret-value-9f2c'],
      names: {},
      apiBase: 'http://localhost:8317',
    });
    const csv = buildUsageCsv(folded, {
      dimension: 'key',
      label: (key) => (key === '*other*' ? 'Other (12 groups)' : labels.get(key) || `#${key}`),
    });
    expect(csv).not.toContain('sk-live-secret-value-9f2c');
    expect(lines(csv)[0]).toStartWith('bucket_start,bucket_end,key,key_label,');
    expect(lines(csv)[1]).toStartWith(
      '2026-10-22T00:00:00Z,2026-10-23T00:00:00Z,*other*,Other (12 groups),40,'
    );
    expect(lines(csv)[2]).toStartWith(
      '2026-10-22T00:00:00Z,2026-10-23T00:00:00Z,a1b2c3d4e5f6,sk-...9f2c,10,'
    );
  });

  test('a user-entered key name cannot inject a formula', () => {
    const folded = normalizeUsageReport(rawFoldedReport());
    const csv = buildUsageCsv(folded, { dimension: 'key', label: () => '=cmd|calc' });
    expect(csv).toContain(",'=cmd|calc,");
    expect(csv).not.toContain(',=cmd');
  });
});

describe('usageCsvFilename', () => {
  test('names the range, unit, group and filters, filename-safe', () => {
    const { query } = normalizeUsageReport(rawReport());
    expect(usageCsvFilename(query, {})).toBe('usage_2026-10-20_2026-10-22_day_model.csv');
    expect(usageCsvFilename(query, { provider: 'claude', model: 'xai/grok 4.7', key: '' })).toBe(
      'usage_2026-10-20_2026-10-22_day_model_claude_xai-grok-4.7.csv'
    );
  });
});
