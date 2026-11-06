// The details panel in Orca's right sidebar. Orca strips the types, compiles
// the JSX and provides the `orca` module; the worker posts the view on each
// poll, so the panel only draws it.
import {
  atom,
  Badge,
  Callout,
  Collapsible,
  EmptyState,
  Heading,
  KeyValue,
  Link,
  List,
  ListItem,
  plugin,
  Progress,
  read,
  RelativeTime,
  Row,
  set,
  Stack,
  Table,
  Text,
  type PanelContext,
} from 'orca';
import type { View } from './view';

const current = atom<View | null>(null);

export function setup(ctx: PanelContext): void {
  ctx.subscriptions.push(plugin.on('view', (v: View) => set(current, v)));
  void plugin
    .request('view.get', {})
    .then((v: View) => set(current, v))
    .catch(() => undefined);
}

function Machine({ v }: { v: View }) {
  if (!v.memory || !v.slots) return null;
  return (
    <Stack gap="sm">
      <Progress label={v.memory.label} value={v.memory.usedPct} />
      <Text size="sm" muted tone={v.memory.tone === 'neutral' ? undefined : v.memory.tone}>
        {v.memory.breakdown}
      </Text>
      <Progress label={v.slots.label} value={v.slots.usedPct} />
    </Stack>
  );
}

function Running({ v }: { v: View }) {
  if (v.running.length === 0) return <Text muted>No heavy command is running.</Text>;
  return (
    <List label="Running">
      {v.running.map((r) => (
        <ListItem key={r.key} title={r.title} description={r.detail}>
          <Row gap="sm" wrap>
            <Text size="sm">{r.memory}</Text>
            <Text size="sm" muted>
              <RelativeTime at={r.since} format="duration" muted />
            </Text>
            {r.left ? (
              <Text size="sm" tone={r.left === 'longer than usual' ? 'warning' : undefined} muted={r.left !== 'longer than usual'}>
                {r.left}
              </Text>
            ) : null}
          </Row>
        </ListItem>
      ))}
    </List>
  );
}

function Waiting({ v }: { v: View }) {
  if (v.waiting.length === 0) return null;
  return (
    <Stack gap="sm">
      <Heading level={2}>Waiting</Heading>
      <List label="Waiting">
        {v.waiting.map((w) => (
          <ListItem key={w.key} title={`#${w.position} ${w.title}`} description={w.detail}>
            <Row gap="sm" wrap>
              <Text size="sm" tone="warning">
                {w.why}
              </Text>
              <Text size="sm" muted>
                waited <RelativeTime at={w.since} format="duration" muted />
              </Text>
              {w.eta ? (
                <Text size="sm" muted>
                  starts in {w.eta}
                </Text>
              ) : null}
            </Row>
          </ListItem>
        ))}
      </List>
    </Stack>
  );
}

function Recent({ v }: { v: View }) {
  if (v.recent.length === 0) return null;
  return (
    <Stack gap="sm">
      {v.guardStops.length > 0 ? (
        <Callout tone="danger" title={`Stopped by a guard (${v.guardStops.length})`}>
          {v.guardStops.map((g) => (
            <Text key={g} size="sm">
              {g}
            </Text>
          ))}
        </Callout>
      ) : null}
      <Collapsible title="Recent" description={`${v.recent.length} ended`} defaultOpen>
      <Table
        label="Recent jobs"
        columns={[
          { key: 'title', label: 'Command', minWidth: 'sm' },
          { key: 'outcome', label: 'Result', minWidth: 'xs' },
          { key: 'where', label: 'Where', hideBelow: 'md' },
          { key: 'peak', label: 'Peak', align: 'end', hideBelow: 'sm' },
          { key: 'took', label: 'Took', align: 'end', hideBelow: 'md' },
          { key: 'ended', label: 'Ended', align: 'end' },
        ]}
        rows={v.recent.map((r) => ({
          key: r.key,
          cells: {
            title: r.title,
            outcome: r.outcome,
            where: r.where,
            peak: r.peak,
            took: r.took,
            ended: { type: 'relative', at: r.ended, format: 'short' },
          },
        }))}
      />
      </Collapsible>
    </Stack>
  );
}

function Repos({ v }: { v: View }) {
  if (v.byRepo.length < 2) return null;
  return (
    <Collapsible title="By repository" description={`${v.byRepo.length} repositories`}>
      <KeyValue label="Jobs by repository" items={v.byRepo.map((r) => ({ label: r.repo, value: r.summary }))} />
    </Collapsible>
  );
}

function Setup({ v }: { v: View }) {
  if (v.problems.length > 0) {
    return (
      <Stack gap="sm">
        {v.problems.map((p) => (
          <Callout key={p.text} tone={p.level === 'error' ? 'danger' : 'warning'} title={p.level === 'error' ? 'Setup error' : 'Setup warning'}>
            <Text>{p.text}</Text>
          </Callout>
        ))}
      </Stack>
    );
  }
  return (
    <Collapsible title="Setup" description={v.checks > 0 ? `All ${v.checks} checks pass` : 'Not checked yet'}>
      <KeyValue label="loadslot" items={v.facts} />
    </Collapsible>
  );
}

export default function Panel() {
  const v = read(current);
  if (!v) return <EmptyState title="Reading loadslot status…" />;
  if (v.problem) {
    return (
      <Stack gap="md">
        <Heading>{v.headline}</Heading>
        <Callout tone="warning" title={v.problem.title}>
          <Text muted>{v.problem.detail}</Text>
          <Link href={v.problem.link}>How to install loadslot</Link>
        </Callout>
      </Stack>
    );
  }
  return (
    <Stack gap="md">
      <Row justify="between">
        <Heading>{v.headline}</Heading>
        <Badge tone={v.tone}>{v.slots?.label.split(' · ')[0] ?? ''}</Badge>
      </Row>
      <Machine v={v} />
      <Heading level={2}>Running</Heading>
      <Running v={v} />
      <Waiting v={v} />
      <Recent v={v} />
      <Repos v={v} />
      <Setup v={v} />
    </Stack>
  );
}
