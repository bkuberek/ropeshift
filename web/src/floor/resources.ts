import type { MachineResources, ResourceReport, SessionResources } from "../api/types";

// bytes says a size in binary units with one decimal, as the worker's own words do.
export function bytes(value: number): string {
  const units = ["KiB", "MiB", "GiB", "TiB"];
  if (value < 1024) {
    return `${value} B`;
  }
  let scaled = value / 1024;
  let unit = units[0];
  for (const next of units.slice(1)) {
    if (scaled < 1024) {
      break;
    }
    scaled /= 1024;
    unit = next;
  }
  return `${scaled.toFixed(1)} ${unit}`;
}

// MachineMeasures are what a person reads of a machine at a glance, as its worker last measured it. Each is
// undefined when the machine could not provide it, which reads as unavailable and never as zero.
export type MachineMeasures = {
  cpu?: string;
  memory?: string;
  // memoryShare is the memory in use as a share of the total, where there is room for one figure only.
  memoryShare?: string;
  pressure?: string;
  // disk is the free space of the fullest volume, which runs out first.
  disk?: string;
  // load is the five-minute load over the cores, which the load warning reads.
  load?: string;
};

export function machineMeasures(machine: MachineResources): MachineMeasures {
  const { cpu_percent: cpu, memory_used_bytes: used, memory_total_bytes: total, memory_pressure_some: pressure, load_5: load, cores } = machine;
  const memory = used !== undefined && total !== undefined && total > 0;
  return {
    cpu: cpu === undefined ? undefined : `${Math.round(cpu)}%`,
    memory: memory ? `${bytes(used)} of ${bytes(total)}` : undefined,
    memoryShare: memory ? `${Math.round((used / total) * 100)}%` : undefined,
    pressure: pressure === undefined ? undefined : `${pressure.toFixed(1)}%`,
    disk: machine.volumes.length ? bytes(Math.min(...machine.volumes.map((volume) => volume.free_bytes))) : undefined,
    load: load === undefined || !cores ? undefined : (load / cores).toFixed(2),
  };
}

// sessionOf finds one session in a worker's last report: a lane by its ID, or the orchestrator.
export function sessionOf(report: ResourceReport | undefined, kind: SessionResources["kind"], lane?: string): SessionResources | undefined {
  return report?.sessions.find((row) => row.kind === kind && (lane === undefined || row.lane === lane));
}
