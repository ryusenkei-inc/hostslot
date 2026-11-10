// Checks imports and package.json dependencies against the layer rules in `lib/deps-rules.ts`
import { runDepsCheck } from '@ryusenkei/conventions';
import { EXCEPTIONS, RULES } from './lib/deps-rules.ts';

runDepsCheck({ rules: RULES, exceptions: EXCEPTIONS });
