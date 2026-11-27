// `legacy` is the label written before the pt-BR translation. Tokens with it still
// appear in drafts and conversation history, so they are still parsed (and typing
// it still triggers the category). server/chat_mentions.go must stay in sync.
export const mentionKinds = [
  { kind: "finding", label: "Vulnerabilidade", alias: "finding", legacy: "漏洞" },
  { kind: "asset", label: "Ativo", alias: "asset", legacy: "资产" },
  { kind: "company", label: "Empresa", alias: "company", legacy: "企业" },
  { kind: "endpoint", label: "Endpoint", alias: "api", legacy: "接口" },
  { kind: "ip", label: "IP", alias: "ip", legacy: "IP" },
  { kind: "app", label: "App", alias: "app", legacy: "应用" },
  { kind: "root_domain", label: "Domínio", alias: "domain", legacy: "域名" },
  { kind: "subdomain", label: "Subdomínio", alias: "subdomain", legacy: "子域名" },
  { kind: "service", label: "Serviço", alias: "service", legacy: "服务" },
] as const;

export type MentionKind = (typeof mentionKinds)[number]["kind"];
export interface ChatMention {
  kind: MentionKind;
  id: number;
  label: string;
  description: string;
}

// Normalizes for prefix matching: lower case and no accents, so "dominio" finds "Domínio".
function fold(value: string) {
  return value.normalize("NFD").replace(/[̀-ͯ]/g, "").toLowerCase();
}

export function activeMention(value: string, caret: number) {
  const before = value.slice(0, caret);
  const start = before.lastIndexOf("@");
  if (start < 0 || (start > 0 && /[\w.+/-]/.test(before[start - 1]))) return null;
  const query = before.slice(start + 1);
  if (/[[\]\r\n@]/.test(query) || query.length > 220) return null;
  return { start, end: caret, query };
}

export function mentionSearch(query: string) {
  const raw = query.trimStart();
  const text = fold(raw);
  for (const item of mentionKinds) {
    // Longest first, so "subdominio" is not cut short by a shorter alias.
    const aliases = [fold(item.label), item.alias, item.legacy.toLowerCase()].sort((a, b) => b.length - a.length);
    for (const alias of aliases) {
      // Latin aliases need a space after them ("ip 1.2.3.4"); CJK ones may be glued to the query.
      if (text === alias || text.startsWith(`${alias} `) || (/[^a-z]/.test(alias) && text.startsWith(alias))) {
        return { kind: item.kind, query: raw.slice(alias.length).trim(), categories: [] };
      }
    }
  }
  const categories = mentionKinds.filter(
    (item) => fold(item.label).startsWith(text) || item.alias.startsWith(text) || item.legacy.startsWith(text),
  );
  return { kind: "" as const, query: query.trim(), categories };
}

export function mentionToken(item: ChatMention) {
  const kind = mentionKinds.find((entry) => entry.kind === item.kind)?.label ?? "Ativo";
  const label = item.label
    .replace(/[[\]]/g, (char) => (char === "[" ? "（" : "）"))
    .replace(/\s+/g, " ")
    .slice(0, 100);
  return `@[${kind}#${item.id} ${label}]`;
}

const tokenLabels = [...new Set(mentionKinds.flatMap((item) => [item.label, item.legacy]))].join("|");
const tokenPattern = new RegExp(`@\\[(${tokenLabels})#([0-9]+)(?: ([^\\]\\r\\n]*))?\\]`, "g");

export function selectedMentions(value: string) {
  return [...value.matchAll(tokenPattern)].map((match) => {
    const kind = mentionKinds.find((item) => item.label === match[1] || item.legacy === match[1]);
    return {
      token: match[0],
      label: `${kind?.label ?? match[1]} #${match[2]}${match[3] ? ` · ${match[3]}` : ""}`,
      start: match.index,
    };
  });
}
