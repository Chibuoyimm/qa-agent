import type { Locator, Page } from 'playwright';
import type { Step } from './contract.js';

// Exact matching and Playwright strictness prevent choosing an arbitrary duplicate.
export function stepLocator(page: Page, step: Pick<Step, 'test_id' | 'locator'>): Locator {
  const root = step.locator?.within_dialog ? page.getByRole('dialog', {name:step.locator.within_dialog, exact:true}) : page;
  if (step.locator?.label) return root.getByLabel(step.locator.label, { exact: true });
  if (step.locator?.role) return root.getByRole(step.locator.role, { name: step.locator.name!, exact: true });
  return page.getByTestId(step.test_id!);
}
