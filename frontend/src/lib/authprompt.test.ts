// Tests for the keyboard-interactive auth-prompt field classification — the
// logic that decides where to offer a suggested password and which field to
// mirror the confirmation into.

import { test, describe } from "node:test";
import assert from "node:assert/strict";

import {
	classifyFields,
	isNewPasswordPrompt,
	isConfirmPasswordPrompt,
	isPasswordChange,
	newPasswordIndex,
	type AuthField,
} from "./authprompt.ts";

describe("isNewPasswordPrompt", () => {
	for (const p of ["New password: ", "Enter new password", "Choose a new password:", "NEW PASSWORD"]) {
		test(`matches ${JSON.stringify(p)}`, () => assert.equal(isNewPasswordPrompt(p), true));
	}
	for (const p of ["Password: ", "Current password:", "Verification code:"]) {
		test(`ignores ${JSON.stringify(p)}`, () => assert.equal(isNewPasswordPrompt(p), false));
	}
});

describe("isConfirmPasswordPrompt", () => {
	for (const p of ["Retype new password: ", "Re-type password", "Confirm password:", "Enter it again"]) {
		test(`matches ${JSON.stringify(p)}`, () => assert.equal(isConfirmPasswordPrompt(p), true));
	}
});

describe("classifyFields", () => {
	test("the standard PAM password-change round", () => {
		const qs: AuthField[] = [
			{ prompt: "Current password: ", echo: false },
			{ prompt: "New password: ", echo: false },
			{ prompt: "Retype new password: ", echo: false },
		];
		assert.deepEqual(classifyFields(qs), ["other", "new", "confirm"]);
		assert.equal(newPasswordIndex(classifyFields(qs)), 1);
		assert.equal(isPasswordChange(classifyFields(qs)), true);
	});

	test("an echoed field is never a password", () => {
		const qs: AuthField[] = [{ prompt: "New password hint (echoed)", echo: true }];
		assert.deepEqual(classifyFields(qs), ["other"]);
		assert.equal(isPasswordChange(classifyFields(qs)), false);
	});

	test("a plain TOTP round is not a password change", () => {
		const qs: AuthField[] = [{ prompt: "Verification code: ", echo: false }];
		assert.deepEqual(classifyFields(qs), ["other"]);
		assert.equal(newPasswordIndex(classifyFields(qs)), -1);
		assert.equal(isPasswordChange(classifyFields(qs)), false);
	});
});
