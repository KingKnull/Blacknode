/**
 * Frontend presentation helpers for the command policy.
 *
 * The backend remains authoritative for classification and enforcement. These
 * helpers only identify the host labels that the UI should surface and turn a
 * returned denial into an actionable explanation.
 */

export type PolicyHost = {
  name?: string;
  environment?: string | null;
  group?: string | null;
  tags?: string[] | null;
};

export type PolicyHostSummary = {
  production: string[];
  readOnly: string[];
};

export type PolicyBlock = {
  reason: string;
  action: string;
};

function policyLabel(value: string | null | undefined): string {
  return (value ?? "").toLowerCase().replace(/[^a-z0-9]/g, "");
}

/** Match the same additive read-only labels understood by the backend. */
export function isReadOnlyHost(host: PolicyHost): boolean {
  const labels = [host.environment, host.group, ...(host.tags ?? [])].map(policyLabel);
  return labels.includes("readonly") || labels.includes("readonlyhost");
}

export function isProductionHost(host: PolicyHost): boolean {
  return policyLabel(host.environment) === "production";
}

/** Return deduplicated display names for the policy-sensitive host groups. */
export function summarizePolicyHosts(hosts: PolicyHost[]): PolicyHostSummary {
  const production = new Set<string>();
  const readOnly = new Set<string>();
  for (const host of hosts) {
    const name = host.name?.trim() || "Unnamed host";
    if (isProductionHost(host)) production.add(name);
    if (isReadOnlyHost(host)) readOnly.add(name);
  }
  return { production: [...production], readOnly: [...readOnly] };
}

/**
 * Recognize the stable backend error prefix and provide next-step guidance.
 * Non-policy failures deliberately return null so connection/command errors
 * keep their existing presentation.
 */
export function policyBlock(error: string | null | undefined): PolicyBlock | null {
  const message = (error ?? "").trim();
  if (!/^command policy denied:/i.test(message)) return null;

  const reason = message.replace(/^command policy denied:\s*/i, "").trim() || "The host policy rejected this command.";
  const lower = reason.toLowerCase();
  const action = lower.includes("read-only host")
    ? "Choose a read-only inspection command or remove the read-only host from the selection."
    : lower.includes("requires explicit approval")
      ? "Review the command and run it again to open the production approval prompt."
      : "Review the command and selected hosts, then retry after resolving the policy requirement.";
  return { reason, action };
}
