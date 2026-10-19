import { afterEach, beforeEach } from 'bun:test';
import i18n from '@/i18n';

// All test files share one i18next instance in one bun process. Without a
// reset, a language that one file sets stays active in the files after it.
// The module-level and afterEach resets give module scope and beforeAll hooks
// of the next file English. The beforeEach reset runs after the beforeAll hooks
// of a file, so a test that needs another language sets it in the test or in
// its own beforeEach.
await i18n.changeLanguage('en');
beforeEach(() => i18n.changeLanguage('en'));
afterEach(() => i18n.changeLanguage('en'));
