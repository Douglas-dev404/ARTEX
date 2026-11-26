// Tabela de campos por canal e utilitários de interpretação dos valores de configuração.
//
// Fica separada da página porque este arquivo contém **dados** e não view: descreve quais campos cada tipo de canal tem,
// qual controle cada um deve usar e a conversão nos dois sentidos entre o texto do formulário e o valor de configuração (JSON).
// Com um arquivo próprio, adicionar um canal novo exige mexer só aqui, sem alterar a página.
// Nome de exibição e descrição de cada tipo de canal. Fica no frontend porque afeta só os textos; o backend não precisa saber.
export const KIND_LABEL: Record<string, string> = {
  dingtalk: "DingTalk",
  feishu: "Feishu (Lark)",
  wecom: "WeCom (WeChat Work)",
  webhook: "Webhook genérico",
  telegram: "Telegram",
  email: "E-mail",
};

// Definição dos campos de configuração de cada canal.
//
// Mantemos de propósito uma tabela de campos própria no frontend, em vez de fazer o backend enviar um schema: o backend cuida apenas de
// Validate (obrigatório/formato), enquanto a UI precisa de layout e tipo de controle; as duas coisas não são o mesmo assunto.
// O único ponto de acoplamento é secret_keys: quais campos devem ser renderizados como caixa de senha vem do backend,
// porque só a implementação do canal sabe quais valores contam como credencial (no WeCom o Webhook inteiro é a credencial,
// já no DingTalk é apenas um dos segredos). Ao adicionar um canal, faltar uma entrada aqui só deixa o formulário em branco,
// sem falha silenciosa (o hasFields abaixo avisa).
export type FieldKind = "text" | "password" | "number" | "select" | "textarea" | "switch" | "kv" | "list";
export interface FieldDef {
  key: string;
  label: string;
  kind: FieldKind;
  placeholder?: string;
  help?: string;
  options?: { value: string; label: string }[];
}
export const CHANNEL_FIELDS: Record<string, FieldDef[]> = {
  dingtalk: [
    {
      key: "webhook",
      label: "Endereço do Webhook",
      kind: "text",
      placeholder: "https://oapi.dingtalk.com/robot/send?access_token=...",
    },
    {
      key: "secret",
      label: "Chave de assinatura",
      kind: "password",
      help: "Preencha quando a configuração de segurança do robô for \"Assinatura\"; deixe vazio se escolher \"Palavras-chave personalizadas\" ou se a segurança não estiver ativada",
    },
  ],
  feishu: [
    {
      key: "webhook",
      label: "Endereço do Webhook",
      kind: "text",
      placeholder: "https://open.feishu.cn/open-apis/bot/v2/hook/...",
    },
    { key: "secret", label: "Chave de verificação de assinatura", kind: "password", help: "Preencha quando o robô tiver \"Verificação de assinatura\" ativada; caso contrário, deixe vazio" },
  ],
  wecom: [
    {
      key: "webhook",
      label: "Endereço do Webhook",
      kind: "text",
      placeholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...",
    },
  ],
  webhook: [
    { key: "url", label: "URL de destino", kind: "text", placeholder: "https://your-endpoint.example.com/hook" },
    {
      key: "method",
      label: "Método da requisição",
      kind: "select",
      options: [
        { value: "POST", label: "POST (com corpo)" },
        { value: "PUT", label: "PUT (com corpo)" },
        { value: "PATCH", label: "PATCH (com corpo)" },
        { value: "GET", label: "GET (sem corpo)" },
      ],
    },
    { key: "headers", label: "Cabeçalhos personalizados", kind: "kv", help: "Um por linha, KEY=VALUE, por exemplo Authorization=Bearer xxx" },
    {
      key: "body_template",
      label: "Modelo do corpo da requisição",
      kind: "textarea",
      help:
        "Deixe vazio para usar o modelo padrão embutido. Variáveis: {{.Title}} {{.Batch}} {{.Count}} {{.HomeURL}} {{.SentAt}}, " +
        "e, dentro de range .Items, .Name/.VulnClass/.Severity/.Summary/.Assets/.DetailURL/.StatusLabel. " +
        "Para inserir strings use {{json .Xxx}} em vez de {{.Xxx}}, senão as aspas no título quebram o JSON.",
    },
  ],
  telegram: [
    { key: "bot_token", label: "Bot Token", kind: "password", placeholder: "123456:ABC-DEF..." },
    { key: "chat_id", label: "Chat ID", kind: "text", placeholder: "-1001234567890" },
    {
      key: "base_url",
      label: "Endereço da API",
      kind: "text",
      placeholder: "https://api.telegram.org",
      help: "Deixe vazio para usar o endereço oficial; preencha ao usar um proxy reverso próprio da Bot API",
    },
  ],
  email: [
    { key: "host", label: "Servidor SMTP", kind: "text", placeholder: "smtp.example.com" },
    {
      key: "port",
      label: "Porta",
      kind: "number",
      placeholder: "587",
      help: "587 usa STARTTLS; para 465, ative \"TLS implícito\"",
    },
    { key: "username", label: "Conta", kind: "text" },
    { key: "password", label: "Senha / código de autorização", kind: "password" },
    { key: "from", label: "Remetente", kind: "text", placeholder: "artex@example.com" },
    { key: "to", label: "Destinatários", kind: "list", help: "Separe vários endereços por vírgula" },
    { key: "tls", label: "TLS implícito", kind: "switch", help: "Ative na porta 465; mantenha desativado na 587 (usa STARTTLS automaticamente)" },
  ],
};

export const SEVERITY_OPTIONS = [
  { value: "", label: "Sem limite" },
  { value: "low", label: "Baixa ou superior" },
  { value: "medium", label: "Média ou superior" },
  { value: "high", label: "Alta ou superior" },
  { value: "critical", label: "Somente crítica" },
];

export type ChannelForm = {
  name: string;
  kind: string;
  mode: "realtime" | "digest";
  enabled: boolean;
  ratePerMin: string;
  config: Record<string, unknown>;
  minSeverity: string;
  includeText: string;
  excludeText: string;
  taskIDsText: string;
  assetIDsText: string;
  onStatusChange: boolean;
};

export const emptyForm = (kind: string): ChannelForm => ({
  name: "",
  kind,
  mode: "realtime",
  enabled: true,
  ratePerMin: "",
  config: {},
  minSeverity: "",
  includeText: "",
  excludeText: "",
  taskIDsText: "",
  assetIDsText: "",
  onStatusChange: false,
});

// parseKV interpreta a área de texto "uma linha KEY=VALUE por linha".
export function parseKV(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (!t) continue;
    const i = t.indexOf("=");
    if (i > 0) out[t.slice(0, i).trim()] = t.slice(i + 1).trim();
  }
  return out;
}
// parseIDs interpreta uma lista de ids separados por vírgula/espaço.
export function parseIDs(text: string): number[] {
  return text
    .split(/[\s,，]+/)
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => Number(s))
    .filter((n) => Number.isFinite(n) && n > 0);
}
// parseKeywords interpreta uma lista de palavras-chave separadas por linha/vírgula (nomes de tipos de vulnerabilidade podem conter espaços, por isso o corte é por linha ou vírgula).
export function parseKeywords(text: string): string[] {
  return text
    .split(/[\n,，]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
