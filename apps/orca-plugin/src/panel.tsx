// The details panel in Orca's right sidebar. Orca strips the types, compiles
// the JSX and provides the `orca` module; the worker posts the view on each
// poll, so the panel only draws it.
import {
  atom,
  Badge,
  Button,
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

function GuardStops({ v }: { v: View }) {
  const count = v.guardStops.reduce((n, g) => n + g.rows.length + g.more, 0);
  if (count === 0) return null;
  return (
    <Callout tone="danger" title={`Stopped by a guard (${count})`}>
      <Stack gap="md">
        {v.guardStops.map((g) => (
          <Stack key={g.key} gap="sm">
            <Text weight="semibold">{g.title}</Text>
            <Text size="sm" muted>
              {g.explanation}
            </Text>
            <List label={g.title}>
              {g.rows.map((r) => (
                <ListItem key={r.key} title={r.title} description={r.where}>
                  <Row gap="sm" wrap>
                    <Text size="sm">peak {r.peak}</Text>
                    <Text size="sm" muted>
                      <RelativeTime at={r.ended} format="short" muted />
                    </Text>
                  </Row>
                </ListItem>
              ))}
            </List>
            {g.more > 0 ? (
              <Text size="sm" muted>
                and {g.more} more, listed in Recent
              </Text>
            ) : null}
          </Stack>
        ))}
      </Stack>
    </Callout>
  );
}

function Recent({ v }: { v: View }) {
  if (v.recent.length === 0) return null;
  return (
    <Stack gap="sm">
      <GuardStops v={v} />
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

// Nothing is downloaded or written until the person presses a button; the
// text says what it downloads, from where, and where it goes.
function Offer({ v }: { v: View }) {
  const o = v.offer;
  if (!o) return null;
  const busy = Boolean(o.progress);
  return (
    <Stack gap="sm">
      {o.install ? (
        <Stack gap="sm">
          <Button variant="solid" tone="accent" disabled={busy} onPress={() => void plugin.request('install', {})}>
            {o.install.update ? `Update loadslot to ${o.install.version}` : `Install loadslot ${o.install.version}`}
          </Button>
          <Text size="sm" muted>
            Downloads it from the loadslot releases on GitHub to {o.install.to}, checks its SHA-256, then runs loadslot
            setup to register the hooks of the agents installed here.
          </Text>
        </Stack>
      ) : null}
      {o.setup && !o.install ? (
        <Button variant="outline" disabled={busy} onPress={() => void plugin.request('setup', {})}>
          Run loadslot setup
        </Button>
      ) : null}
      {o.progress ? (
        <Text size="sm" muted>
          {o.progress}
        </Text>
      ) : null}
      {o.result ? (
        <Callout tone={o.result.ok ? 'success' : 'danger'} title={o.result.title}>
          {o.result.items.length > 0 ? <KeyValue label={o.result.title} items={o.result.items} /> : null}
          {o.result.text ? <Text size="sm">{o.result.text}</Text> : null}
        </Callout>
      ) : null}
      {o.result?.next ? (
        <Callout tone="warning" title="One more step">
          <Text size="sm">{o.result.next}</Text>
        </Callout>
      ) : null}
    </Stack>
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
        <Offer v={v} />
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
      <Offer v={v} />
      <Setup v={v} />
    </Stack>
  );
}
