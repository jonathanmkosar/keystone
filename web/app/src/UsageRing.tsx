// The usage ring in a chat's header on a phone (the owner, November 27, 2026,
// after another app's header): a ring that fills as the month's spend nears
// the monthly cap. A tap opens a sheet with the month's spend against the
// cap and the memory pass's reserve, and a way to Settings, Usage, where the
// whole report is (UsageSettings.tsx). It reads the same report, GET
// /api/v1/usage, and reads it again when a turn in the chat ends and when the
// sheet opens. The look is usage-ring.css; the bars are usage.css's.

import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Icon } from "./Icon";
import { Sheet } from "./PhoneSheet";
import { isActive, useLive } from "./turns";
import { readUsage, type UsageReport } from "./usageCalls";
import { capShare, clamp, reserveShare, resets, type Share as CapShare } from "./usageShare";
import "./usage.css";
import "./usage-ring.css";

/** An amount in millionths of a dollar, to the cent. */
function dollars(micros: number): string {
  if (micros > 0 && micros < 5000) return "under $0.01";
  return "$" + (micros / 1e6).toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

function Ring({ share, tone }: CapShare) {
  return (
    <svg className="usage-ring" width="24" height="24" viewBox="0 0 24 24" aria-hidden="true">
      <circle className="usage-ring-track" cx="12" cy="12" r="9" />
      {share > 0 && <circle className={`usage-ring-fill is-${tone}`} cx="12" cy="12" r="9" pathLength="100" strokeDasharray={`${Math.max(share * 100, 3)} 100`} />}
    </svg>
  );
}

function Bar({ share, tone }: CapShare) {
  const width = clamp(share) * 1000;
  return (
    <svg className="use-bar usage-sheet-bar" viewBox="0 0 1000 10" preserveAspectRatio="none" aria-hidden="true">
      <rect className="use-bar-track" x="0" y="0" width="1000" height="10" />
      {width > 0 && <rect className={`use-bar-fill is-${tone}`} x="0" y="0" width={Math.max(width, 6)} height="10" />}
    </svg>
  );
}

function percent(share: number): string {
  return `${Math.round(share * 100)}% used`;
}

/** The ring, and the sheet it opens. chatId is the open chat, whose turns ending read the report again. */
export function UsageButton({ chatId }: { chatId: string }) {
  const [report, setReport] = useState<UsageReport | null>(null);
  const [failed, setFailed] = useState(false);
  const [open, setOpen] = useState(false);
  const opener = useRef<HTMLButtonElement>(null);
  const running = isActive(useLive(chatId));

  const read = useCallback(() => {
    readUsage({ period: "this_month" }).then(
      (r) => {
        setReport(r);
        setFailed(false);
      },
      () => setFailed(true),
    );
  }, []);

  // On the way in, and each time a turn in the chat ends.
  useEffect(() => {
    if (!running) read();
  }, [running, read]);

  const close = useCallback(() => setOpen(false), []);
  const cap: CapShare = report ? capShare(report.cap) : { share: 0, tone: "ok" };
  const label = report ? `Usage: ${Math.round(cap.share * 100)} percent of this month's cap used` : "Usage";

  const reserve = report?.memory_reserve;
  const kept = reserve ? reserveShare(reserve) : null;

  return (
    <>
      <button
        type="button"
        ref={opener}
        className="icon-button usage-button"
        aria-label={label}
        title="Usage"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => {
          setOpen(true);
          read();
        }}
      >
        <Ring {...cap} />
      </button>
      <Sheet open={open} label="Usage" onClose={close} opener={opener}>
        <div className="usage-sheet-head">
          <button type="button" className="usage-round" aria-label="Close" onClick={close}>
            <Icon name="close" size={20} />
          </button>
          <h2 className="usage-sheet-title">Usage</h2>
          <div className="usage-round-pair">
            <button type="button" className="usage-round" aria-label="Read the usage again" title="Read again" onClick={read}>
              <Icon name="retry" size={20} />
            </button>
            <Link to="/settings/usage" className="usage-round" aria-label="All usage, in Settings" title="All usage" onClick={close}>
              <Icon name="open" size={20} />
            </Link>
          </div>
        </div>
        {!report ? (
          <p className="usage-sheet-quiet">{failed ? "Your usage could not be read. Check that the server is running." : "Loading."}</p>
        ) : (
          <>
            <div className="usage-card">
              <div className="usage-card-line">
                <span className="usage-card-name">This month</span>
                <span className="usage-card-used">{report.cap.state === "unread" ? "Cap does not read" : percent(cap.share)}</span>
              </div>
              <Bar {...cap} />
              <p className="usage-card-sub">
                {report.cap.state === "unread" ? `${dollars(report.cap.spent_micros)} spent` : `${dollars(report.cap.spent_micros)} of ${dollars(report.cap.limit_micros)}`}
                {resets(report.cap.month) && ` · Resets ${resets(report.cap.month)}`}
              </p>
            </div>
            {reserve && (
              <>
                <p className="usage-sheet-heading">Memory pass</p>
                <div className="usage-card">
                  <div className="usage-card-line">
                    <span className="usage-card-name">Its reserve</span>
                    {kept && <span className="usage-card-used">{percent(kept.share)}</span>}
                  </div>
                  {kept && <Bar {...kept} />}
                  <p className="usage-card-sub">{reserve.problem ? reserve.problem : `${dollars(reserve.spent_micros)} of ${dollars(reserve.limit_micros)}, a part of the cap`}</p>
                </div>
              </>
            )}
            {failed && <p className="sheet-note">The usage could not be read again. This is what was read before.</p>}
            <p className="usage-sheet-quiet">The cap counts hosted and trusted models; local models cost nothing. The month is a calendar month in UTC.</p>
          </>
        )}
      </Sheet>
    </>
  );
}
