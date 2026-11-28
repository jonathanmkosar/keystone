// Where the sidebar's Waymarks link opens (the owner, November 27, 2026):
// knowledge.waymarks_web_address, or else knowledge.waymarks_address, and
// only while Waymarks is the person's knowledge store. Only an http or https
// address is linked, with no name or password in it, so a setting can never
// become a script link. Pure, so it is tested without a page.

type Named = { key: string; value: string };

/** The address the link opens, or "" when there is none to show. */
export function waymarksLink(settings: Named[]): string {
  const get = (key: string) => settings.find((s) => s.key === key)?.value.trim() ?? "";
  if (get("knowledge.adapter") !== "waymarks") return "";
  const address = get("knowledge.waymarks_web_address") || get("knowledge.waymarks_address");
  if (address === "") return "";
  let url: URL;
  try {
    url = new URL(address);
  } catch {
    return "";
  }
  if ((url.protocol !== "http:" && url.protocol !== "https:") || url.username !== "" || url.password !== "") return "";
  return url.href;
}
