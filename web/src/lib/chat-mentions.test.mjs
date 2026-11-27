import assert from "node:assert/strict";
import test from "node:test";
import { activeMention, mentionSearch, mentionToken, selectedMentions } from "./chat-mentions.ts";

test("mention trigger supports cursor placement without hijacking email", () => {
  assert.equal(activeMention("user@example.com", 16), null);
  assert.equal(activeMention("já citei @[Vulnerabilidade#1 X]", 30), null);
  assert.deepEqual(activeMention("veja @vuln depois", 10), { start: 5, end: 10, query: "vuln" });
  assert.deepEqual(activeMention("查看@漏洞 后面的文字", 5), { start: 2, end: 5, query: "漏洞" });
  assert.equal(activeMention("@vuln\nlinha seguinte", 12), null);
});

test("categories, Portuguese and legacy aliases, IP and keyword search", () => {
  assert.equal(mentionSearch("").categories.length, 9);
  assert.equal(mentionSearch("vul").categories[0].kind, "finding");
  assert.equal(mentionSearch("漏").categories[0].kind, "finding");
  assert.equal(mentionSearch("vulnerabilidade").kind, "finding");
  assert.equal(mentionSearch("Vulnerabilidade SQL injection").query, "SQL injection");
  assert.equal(mentionSearch("漏洞SQL注入").query, "SQL注入");
  assert.equal(mentionSearch("ip 192.0.2.1").kind, "ip");
  assert.equal(mentionSearch("endpoint GET /api").query, "GET /api");
  assert.equal(mentionSearch("dominio acme").kind, "root_domain");
  assert.equal(mentionSearch("Subdomínio www").kind, "subdomain");
  assert.equal(mentionSearch("Subdomínio www").query, "www");
  assert.equal(mentionSearch("acme.com").kind, "");
});

test("tokens roundtrip labels and removing one reference preserves its neighbors", () => {
  const first = mentionToken({ kind: "finding", id: 12, label: "Título[1]\ndescrição" });
  const second = mentionToken({ kind: "ip", id: 13, label: "192.0.2.1" });
  assert.equal(first, "@[Vulnerabilidade#12 Título（1） descrição]");
  const value = `analise ${first} e ${second}`;
  const selected = selectedMentions(value);
  assert.equal(selected.length, 2);
  assert.equal(selected[0].label, "Vulnerabilidade #12 · Título（1） descrição");
  const next = value.slice(0, selected[0].start) + value.slice(selected[0].start + selected[0].token.length);
  assert.equal(selectedMentions(next)[0].token, second);
});

test("legacy Chinese tokens from older conversations are still recognized", () => {
  const selected = selectedMentions("分析 @[漏洞#7 旧标题] e @[子域名#8]");
  assert.equal(selected.length, 2);
  assert.equal(selected[0].label, "Vulnerabilidade #7 · 旧标题");
  assert.equal(selected[1].label, "Subdomínio #8");
});
