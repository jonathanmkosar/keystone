// Location per message on the screen (P10-06): the composer's "Use my
// location" control, a small button beside the attach button on a desktop
// and a row of the + sheet on a phone; the chip over the box while a
// location waits to go with the message; the "Sent with location" mark on
// the person's message; and the inspector's section with the line a turn
// sent. The pure part, and every word, is location.ts.
//
// The browser's position is asked for only in tap(), which only a click on
// the control calls: never on load, never watched. The position is rounded
// in location.ts before this file holds it, and goes with one message: the
// composer takes it when it sends, and the next message has none.
//
// There is no mockup of any of it: it is built from the tokens, after the
// attach button and the files' chips, and is provisional.

import { useEffect, useRef, useState } from "react";
import { listSettings } from "./api";
import { Icon } from "./Icon";
import { askPosition, availability, chipText, distanceWords, hint, sentOf, words, readLocationSettings, type GeoLike, type LocationSettings, type Rounded, type SentPosition } from "./location";
import { SheetRow, useSheet } from "./PhoneSheet";
import "./location.css";

/** What a composer holds of the location that goes with its message. */
export type LocationControl = {
  /** location.share is on and the settings were read: draw the control. */
  shown: boolean;
  settings?: LocationSettings;
  /** The rounded position that waits to go with the message. */
  place?: Rounded;
  /** The browser is being asked. */
  asking: boolean;
  /** Why the last tap gave no position, for a person. Empty otherwise. */
  problem: string;
  /** Whether this page can ask at all, and why not. */
  can: { available: boolean; reason: string };
  /** The control's click: asks the browser once, or stops waiting for it. */
  tap: () => void;
  remove: () => void;
  /** The position for the message being sent, if any. The composer calls it once, as it sends. */
  take: () => SentPosition | undefined;
};

function browserGeo(): GeoLike | undefined {
  return typeof navigator !== "undefined" && "geolocation" in navigator ? (navigator.geolocation as unknown as GeoLike) : undefined;
}

/** The location of one composer's next message. */
export function useLocation(): LocationControl {
  const [settings, setSettings] = useState<LocationSettings>();
  const [place, setPlace] = useState<Rounded>();
  const [asking, setAsking] = useState(false);
  const [problem, setProblem] = useState("");
  const asked = useRef(0); // the tap whose answer is awaited; a later tap or a removal makes it stale

  // The settings, read each time a composer opens, so a change on Settings
  // is used by the next one. No position is read here.
  useEffect(() => {
    let live = true;
    listSettings().then(
      (r) => live && setSettings(readLocationSettings(r.settings)),
      () => {}, // no settings, no control
    );
    return () => {
      live = false;
    };
  }, []);

  const can = availability(typeof window !== "undefined" && window.isSecureContext, browserGeo());

  return {
    shown: !!settings?.on,
    settings,
    place,
    asking,
    problem,
    can,
    tap: () => {
      if (!settings) return;
      if (asking) {
        asked.current++; // a second tap stops waiting; a late answer is dropped
        setAsking(false);
        return;
      }
      if (!can.available) {
        setProblem(can.reason);
        return;
      }
      const mine = ++asked.current;
      setAsking(true);
      setProblem("");
      void askPosition(browserGeo(), window.isSecureContext, settings).then((r) => {
        if (asked.current !== mine) return;
        setAsking(false);
        if (r.ok) setPlace(r.place);
        else setProblem(r.problem);
      });
    },
    remove: () => {
      asked.current++;
      setAsking(false);
      setPlace(undefined);
      setProblem("");
    },
    take: () => {
      const sent = sentOf(place);
      asked.current++;
      setAsking(false);
      setPlace(undefined);
      setProblem("");
      return sent;
    },
  };
}

/** The control: a small button beside the attach button, or a row of the phone's + sheet. Dimmed with its reason when the page cannot ask. */
export function LocationButton({ location }: { location: LocationControl }) {
  const sheet = useSheet();
  if (!location.shown || !location.settings) return null;
  const d = location.settings.decimals;
  const title = location.can.available ? hint(d) : location.can.reason;
  if (sheet) {
    const state = !location.can.available ? "needs a secure page" : location.asking ? "finding…" : location.place ? `about ${distanceWords(location.place.decimals)}` : "";
    return (
      <div className="sheet-item">
        <SheetRow
          icon="location"
          name={words.name}
          state={state}
          pressed={!!location.place}
          disabled={!location.can.available}
          title={title}
          onClick={() => {
            location.tap();
            sheet.done();
          }}
        />
        <p className="location-sheet-hint">{title}</p>
      </div>
    );
  }
  return (
    <button
      type="button"
      className={"icon-button location-button" + (location.place ? " is-on" : "") + (location.asking ? " is-asking" : "") + (location.can.available ? "" : " is-unavailable")}
      aria-label={location.asking ? words.finding : words.name}
      aria-pressed={!!location.place}
      aria-disabled={!location.can.available || undefined}
      aria-busy={location.asking || undefined}
      title={title}
      onClick={location.tap}
    >
      <Icon name="location" size={18} />
    </button>
  );
}

/** Over the box: the chip of the location that will go with the message, the line while the browser is asked, or why there is no position. */
export function LocationChip({ location }: { location: LocationControl }) {
  if (!location.shown || !location.settings) return null;
  if (!location.place && !location.asking && !location.problem) return null;
  const d = location.place?.decimals ?? location.settings.decimals;
  return (
    <div className="location-row">
      {location.place && (
        <span className="location-chip" title={hint(d)}>
          <Icon name="location" size={14} />
          <span className="location-chip-text">{chipText(d)}</span>
          <button type="button" className="icon-button location-chip-remove" aria-label={words.remove} title={words.remove} onClick={location.remove}>
            <Icon name="close" size={14} />
          </button>
        </span>
      )}
      {location.asking && (
        <p className="location-status" role="status">
          {words.finding}
        </p>
      )}
      {location.problem && (
        <p className="composer-problem" role="alert">
          {location.problem}
        </p>
      )}
    </div>
  );
}

/** Under the person's message: it went with their approximate location. */
export function LocationMark({ title }: { title?: string }) {
  return (
    <p className="said-location" title={title}>
      <Icon name="location" size={14} />
      <span>{words.mark}</span>
    </p>
  );
}

/** The turn inspector's section: the location the turn's message carried, and the line each call sent before the person's words. */
export function LocationSection({ location }: { location?: { latitude: number; longitude: number; decimals: number; line: string } }) {
  if (!location) return null;
  return (
    <section className="inspector-section" aria-label="Location">
      <h3 className="inspector-heading">Location</h3>
      <p className="inspector-note">This message was sent with your approximate location. Each call of this turn carried this line before your words:</p>
      <blockquote className="location-line">{location.line}</blockquote>
    </section>
  );
}
