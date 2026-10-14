// Pure helpers for the keyboard-interactive auth prompt (AuthPrompt.svelte).
//
// Kept framework-free so the field-classification logic — which decides where to
// offer a "Suggest" button and which field to auto-fill with the confirmation —
// can be unit-tested without a DOM.

// How long a captured new password stays eligible to be saved after a forced
// change. The save happens as soon as the triggering connection succeeds, so
// this only guards against a stale value lingering if that never happens.
export const PENDING_PASSWORD_TTL_MS = 5 * 60 * 1000;

// "New password" / "choose a new password" / "enter new password".
const NEW_PW = /new\s+password|choose.*password|enter.*new.*password/i;
// "Retype", "re-type", "confirm", "again", "verify".
const CONFIRM_PW = /re-?type|confirm|again|verify/i;

export interface AuthField {
	prompt: string;
	echo: boolean;
}

export type FieldRole = "new" | "confirm" | "other";

/** A server prompt asking the user to choose a new password. */
export function isNewPasswordPrompt(prompt: string): boolean {
	return NEW_PW.test(prompt);
}

/** A server prompt asking the user to repeat the new password. */
export function isConfirmPasswordPrompt(prompt: string): boolean {
	return CONFIRM_PW.test(prompt);
}

/**
 * Classify each question. Only masked (non-echo) fields can be a new/confirm
 * password — an echoed field is something like "Username". A "Retype new
 * password" field matches both patterns, so confirm is checked first.
 */
export function classifyFields(questions: AuthField[]): FieldRole[] {
	return questions.map((q) => {
		if (q.echo) return "other";
		if (isConfirmPasswordPrompt(q.prompt)) return "confirm";
		if (isNewPasswordPrompt(q.prompt)) return "new";
		return "other";
	});
}

/** Index of the field to put a suggested password in, or -1 if none. */
export function newPasswordIndex(roles: FieldRole[]): number {
	return roles.indexOf("new");
}

/**
 * Whether a round changes the password — i.e. its result is worth saving to the
 * vault once the connection that triggered it succeeds.
 */
export function isPasswordChange(roles: FieldRole[]): boolean {
	return roles.includes("new");
}
