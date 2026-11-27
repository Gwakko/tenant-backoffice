import type { PeriodTotals } from './queries';
import { barPercents, formatSum } from './format';

const BAR = 8;
const GAP = 2;
const GROUP = BAR * 2 + GAP * 2;
const HEIGHT = 100;

// A plain SVG bar chart, no library: credits (left bar) and debits (right bar) per period. The
// text alternative is the aria-label plus the table next to it, which carries every value.
export function SeriesChart({ series, currency }: { series: readonly PeriodTotals[]; currency: string | undefined }) {
  if (series.length === 0) return null;
  // One scale for both bars: percentages are relative to the larger of the two series maxima.
  const all = barPercents([...series.map((point) => point.credits), ...series.map((point) => point.debits)]);
  const creditBars = all.slice(0, series.length);
  const debitBars = all.slice(series.length);
  const first = series[0];
  const last = series.at(-1);
  const label = `Bar chart of credits and debits for ${String(series.length)} periods from ${first?.periodStart ?? ''} to ${last?.periodStart ?? ''}${
    currency ? ` in ${currency}` : ''
  }. The table below lists every value.`;
  return (
    <figure className="rounded border border-slate-200 bg-white p-3">
      <svg
        role="img"
        aria-label={label}
        viewBox={`0 0 ${String(series.length * GROUP)} ${String(HEIGHT)}`}
        preserveAspectRatio="none"
        className="h-40 w-full"
      >
        {series.map((point, index) => {
          const x = index * GROUP + GAP;
          const credit = creditBars[index] ?? 0;
          const debit = debitBars[index] ?? 0;
          return (
            <g key={point.periodStart}>
              <rect x={x} y={HEIGHT - credit} width={BAR} height={credit} className="fill-green-600">
                <title>{`${point.periodStart} credits ${formatSum(point.credits)}`}</title>
              </rect>
              <rect x={x + BAR} y={HEIGHT - debit} width={BAR} height={debit} className="fill-red-600">
                <title>{`${point.periodStart} debits ${formatSum(point.debits)}`}</title>
              </rect>
            </g>
          );
        })}
      </svg>
      <figcaption className="mt-1 flex gap-4 text-xs text-slate-600">
        <span>
          <span aria-hidden="true" className="mr-1 inline-block h-2 w-2 bg-green-600" />
          Credits
        </span>
        <span>
          <span aria-hidden="true" className="mr-1 inline-block h-2 w-2 bg-red-600" />
          Debits
        </span>
      </figcaption>
    </figure>
  );
}
