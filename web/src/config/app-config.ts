import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "ARTEX",
  version: packageJson.version,
  copyright: `© ${currentYear}, ARTEX.`,
  meta: {
    title: "ARTEX — Console de Pentest Autônomo",
    description: "Console do sistema de pentest autônomo orientado por LLM",
  },
};
