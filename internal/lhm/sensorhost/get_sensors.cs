#nullable enable
using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text;
using LibreHardwareMonitor.Hardware;

namespace MonitorAgent.Lhm;

internal sealed class UpdateVisitor : IVisitor
{
    public void VisitComputer(IComputer computer) => computer.Traverse(this);
    public void VisitHardware(IHardware hardware)
    {
        hardware.Update();
        foreach (var sub in hardware.SubHardware)
            sub.Accept(this);
    }
    public void VisitSensor(ISensor sensor) { }
    public void VisitParameter(IParameter parameter) { }
}

internal sealed class ServerMetricsSnapshot
{
    public double? CpuTotalPercent;
    public double? CpuPowerWatts;
    public List<CoreEntry> Cores = new();
    public MemoryEntry Memory = new();
    public List<TemperatureEntry> Temperatures = new();
}

internal sealed class CoreEntry
{
    public string Cpu = "";
    public double UsagePercent;
}

internal sealed class MemoryEntry
{
    public long? Used;
    public long? Available;
    public long? Total;
}

internal sealed class TemperatureEntry
{
    public string Sensor = "";
    public double Celsius;
}

internal static class Program
{
    private static Computer? _computer;
    private static readonly UpdateVisitor Visitor = new();

    static void Main()
    {
        _computer = new Computer
        {
            IsCpuEnabled = true,
            IsGpuEnabled = true,
            IsMemoryEnabled = true,
            IsMotherboardEnabled = true,
            IsStorageEnabled = true,
        };
        _computer.Open();

        var reader = Console.In;
        var writer = Console.Out;

        string? line;
        while ((line = reader.ReadLine()) != null)
        {
            if (!line.Trim().Equals("getServerMetrics", StringComparison.OrdinalIgnoreCase))
                continue;

            var snapshot = CollectSnapshot(_computer);
            writer.WriteLine(Serialize(snapshot));
            writer.Flush();
        }

        _computer.Close();
    }

    static ServerMetricsSnapshot CollectSnapshot(Computer computer)
    {
        computer.Accept(Visitor);

        var snapshot = new ServerMetricsSnapshot();
        var coreLoads = new SortedDictionary<int, double>();

        foreach (var hardware in computer.Hardware)
        {
            CollectHardware(hardware, snapshot, coreLoads);
            foreach (var sub in hardware.SubHardware)
                CollectHardware(sub, snapshot, coreLoads);
        }

        if (!snapshot.CpuTotalPercent.HasValue && coreLoads.Count > 0)
            snapshot.CpuTotalPercent = coreLoads.Values.Average();

        if (snapshot.CpuPowerWatts.HasValue && snapshot.CpuPowerWatts.Value < 0)
            snapshot.CpuPowerWatts = null;

        foreach (var kv in coreLoads)
        {
            snapshot.Cores.Add(new CoreEntry
            {
                Cpu = kv.Key.ToString(CultureInfo.InvariantCulture),
                UsagePercent = kv.Value,
            });
        }

        return snapshot;
    }

    static void CollectHardware(IHardware hardware, ServerMetricsSnapshot snapshot, SortedDictionary<int, double> coreLoads)
    {
        if (hardware.HardwareType == HardwareType.Memory)
            CollectMemory(hardware, snapshot.Memory);

        foreach (var sensor in hardware.Sensors)
        {
            if (!sensor.Value.HasValue)
                continue;

            var value = sensor.Value.Value;

            if (sensor.SensorType == SensorType.Temperature)
            {
                if (!IsReportedTemperature(sensor.Name, value))
                    continue;
                snapshot.Temperatures.Add(new TemperatureEntry
                {
                    Sensor = TemperatureSensorKey(hardware, sensor),
                    Celsius = value,
                });
                continue;
            }

            if (hardware.HardwareType == HardwareType.Cpu && sensor.SensorType == SensorType.Power)
            {
                if (IsPackagePower(sensor.Name))
                    snapshot.CpuPowerWatts = (snapshot.CpuPowerWatts ?? 0) + value;
                continue;
            }

            if (hardware.HardwareType != HardwareType.Cpu || sensor.SensorType != SensorType.Load)
                continue;

            if (TryParseCoreIndex(sensor.Name, out var coreIndex))
            {
                coreLoads[coreIndex] = value;
                continue;
            }

            if (IsTotalCpuLoad(sensor.Name))
                snapshot.CpuTotalPercent = value;
        }
    }

    static void CollectMemory(IHardware hardware, MemoryEntry memory)
    {
        foreach (var sensor in hardware.Sensors)
        {
            if (!sensor.Value.HasValue)
                continue;

            var name = sensor.Name;
            var bytes = MemoryBytes(sensor, sensor.Value.Value);
            if (bytes == null)
                continue;

            if (name.Contains("Used", StringComparison.OrdinalIgnoreCase) &&
                !name.Contains("Swap", StringComparison.OrdinalIgnoreCase))
                memory.Used = bytes;
            else if (name.Contains("Available", StringComparison.OrdinalIgnoreCase))
                memory.Available = bytes;
            else if (name.Equals("Memory", StringComparison.OrdinalIgnoreCase) ||
                     name.Contains("Total", StringComparison.OrdinalIgnoreCase))
                memory.Total = bytes;
        }

        if (memory.Total == null && memory.Used != null && memory.Available != null)
            memory.Total = memory.Used.Value + memory.Available.Value;
    }

    static long? MemoryBytes(ISensor sensor, float value)
    {
        return sensor.SensorType switch
        {
            SensorType.Data => (long)(value * 1024L * 1024L * 1024L),
            SensorType.SmallData => (long)(value * 1024L * 1024L),
            _ => null,
        };
    }

    static bool TryParseCoreIndex(string name, out int index)
    {
        index = -1;
        foreach (var prefix in new[] { "CPU Core #", "Core #" })
        {
            if (!name.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
                continue;
            var rest = name.Substring(prefix.Length);
            var space = rest.IndexOf(' ');
            if (space > 0)
                rest = rest.Substring(0, space);
            if (int.TryParse(rest, NumberStyles.Integer, CultureInfo.InvariantCulture, out index))
                return true;
        }
        return false;
    }

    static bool IsTotalCpuLoad(string name)
    {
        if (name.Equals("CPU Total", StringComparison.OrdinalIgnoreCase))
            return true;
        if (name.Contains("Total CPU", StringComparison.OrdinalIgnoreCase))
            return true;
        if (name.Equals("CPU", StringComparison.OrdinalIgnoreCase))
            return true;
        return false;
    }

    static bool IsPackagePower(string name)
    {
        if (name.Contains("Core", StringComparison.OrdinalIgnoreCase))
            return false;
        if (name.Contains("DRAM", StringComparison.OrdinalIgnoreCase))
            return false;
        if (name.Contains("GT ", StringComparison.OrdinalIgnoreCase))
            return false;
        if (name.Contains("Graphics", StringComparison.OrdinalIgnoreCase))
            return false;
        if (name.Contains("SOC", StringComparison.OrdinalIgnoreCase) &&
            !name.Contains("Package", StringComparison.OrdinalIgnoreCase))
            return false;
        if (name.Equals("Package", StringComparison.OrdinalIgnoreCase))
            return true;
        if (name.Contains("Package", StringComparison.OrdinalIgnoreCase))
            return true;
        if (name.Equals("CPU Power", StringComparison.OrdinalIgnoreCase))
            return true;
        return false;
    }

    static bool IsReportedTemperature(string name, float celsius)
    {
        if (celsius <= 0)
            return false;
        if (name.Contains("Distance", StringComparison.OrdinalIgnoreCase))
            return false;
        // LHM exposes NVMe/RAM threshold limits as Temperature sensors (see LHM PR #2124).
        if (name.Contains("Warning Temperature", StringComparison.OrdinalIgnoreCase))
            return false;
        if (name.Contains("Critical Temperature", StringComparison.OrdinalIgnoreCase))
            return false;
        return true;
    }

    static string TemperatureSensorKey(IHardware hardware, ISensor sensor)
    {
        var hw = hardware.Name;
        if (string.IsNullOrWhiteSpace(hw))
        {
            hw = hardware.Identifier.ToString().Replace("/", "_").TrimStart('_');
        }
        return hw + "/" + sensor.Name;
    }

    static string Serialize(ServerMetricsSnapshot s)
    {
        var sb = new StringBuilder(512);
        sb.Append('{');

        sb.Append("\"cpuTotalPercent\":");
        AppendNullableDouble(sb, s.CpuTotalPercent);

        sb.Append(",\"cpuPowerWatts\":");
        AppendNullableDouble(sb, s.CpuPowerWatts);

        sb.Append(",\"cores\":[");
        for (var i = 0; i < s.Cores.Count; i++)
        {
            if (i > 0) sb.Append(',');
            var c = s.Cores[i];
            sb.Append("{\"cpu\":");
            AppendString(sb, c.Cpu);
            sb.Append(",\"usagePercent\":");
            AppendDouble(sb, c.UsagePercent);
            sb.Append('}');
        }
        sb.Append(']');

        sb.Append(",\"memory\":{");
        sb.Append("\"used\":");
        AppendNullableLong(sb, s.Memory.Used);
        sb.Append(",\"available\":");
        AppendNullableLong(sb, s.Memory.Available);
        sb.Append(",\"total\":");
        AppendNullableLong(sb, s.Memory.Total);
        sb.Append('}');

        sb.Append(",\"temperatures\":[");
        for (var i = 0; i < s.Temperatures.Count; i++)
        {
            if (i > 0) sb.Append(',');
            var t = s.Temperatures[i];
            sb.Append("{\"sensor\":");
            AppendString(sb, t.Sensor);
            sb.Append(",\"celsius\":");
            AppendDouble(sb, t.Celsius);
            sb.Append('}');
        }
        sb.Append("]}");

        return sb.ToString();
    }

    static void AppendString(StringBuilder sb, string value)
    {
        sb.Append('"');
        foreach (var ch in value)
        {
            switch (ch)
            {
                case '\\': sb.Append("\\\\"); break;
                case '"': sb.Append("\\\""); break;
                case '\n': sb.Append("\\n"); break;
                case '\r': sb.Append("\\r"); break;
                case '\t': sb.Append("\\t"); break;
                default:
                    if (ch < 0x20)
                        sb.AppendFormat(CultureInfo.InvariantCulture, "\\u{0:X4}", (int)ch);
                    else
                        sb.Append(ch);
                    break;
            }
        }
        sb.Append('"');
    }

    static void AppendDouble(StringBuilder sb, double value) =>
        sb.Append(value.ToString("0.########", CultureInfo.InvariantCulture));

    static void AppendNullableDouble(StringBuilder sb, double? value) =>
        sb.Append(value.HasValue ? value.Value.ToString("0.########", CultureInfo.InvariantCulture) : "null");

    static void AppendNullableLong(StringBuilder sb, long? value) =>
        sb.Append(value.HasValue ? value.Value.ToString(CultureInfo.InvariantCulture) : "null");
}
