using System;
using System.ComponentModel;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Text;
using System.IO;
using Microsoft.Win32.SafeHandles;

// The owning PowerShell process keeps the only handle. Windows closes it even
// when the console is forcibly closed, reclaiming every inherited child process.
public static class RunDockDevJob {
    private static IntPtr job;
    [StructLayout(LayoutKind.Sequential)]
    private struct BasicLimits {
        public long PerProcessUserTimeLimit, PerJobUserTimeLimit;
        public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize, MaximumWorkingSetSize;
        public uint ActiveProcessLimit;
        public UIntPtr Affinity;
        public uint PriorityClass, SchedulingClass;
    }
    [StructLayout(LayoutKind.Sequential)]
    private struct IoCounters {
        public ulong ReadOperationCount, WriteOperationCount, OtherOperationCount;
        public ulong ReadTransferCount, WriteTransferCount, OtherTransferCount;
    }
    [StructLayout(LayoutKind.Sequential)]
    private struct ExtendedLimits {
        public BasicLimits Basic;
        public IoCounters Io;
        public UIntPtr ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed;
    }
    [DllImport("kernel32.dll", SetLastError=true)]
    private static extern IntPtr CreateJobObject(IntPtr attributes, string name);
    [DllImport("kernel32.dll", SetLastError=true)]
    private static extern bool SetInformationJobObject(IntPtr job, int infoClass, ref ExtendedLimits info, uint size);
    [DllImport("kernel32.dll", SetLastError=true)]
    private static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    [DllImport("kernel32.dll")]
    private static extern bool CloseHandle(IntPtr handle);

    [DllImport("kernel32.dll", CharSet=CharSet.Unicode)]
    private static extern int GetCurrentPackageFullName(ref uint length, StringBuilder name);

    public static bool IsPackaged() {
        uint length = 0;
        // ERROR_INSUFFICIENT_BUFFER means a package identity exists.
        return GetCurrentPackageFullName(ref length, null) == 122;
    }

    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    private static extern uint GetFinalPathNameByHandle(SafeFileHandle file, StringBuilder path, uint size, uint flags);

    public static string DatabasePath(string file) {
        if (!File.Exists(file)) return "";
        using (var stream = new FileStream(file, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete)) {
            var path = new StringBuilder(32768);
            uint size = GetFinalPathNameByHandle(stream.SafeFileHandle, path, (uint)path.Capacity, 0);
            if (size == 0 || size >= path.Capacity) throw new Win32Exception();
            return path.ToString();
        }
    }

    public static bool HasVirtualizedData(string file) {
        if (IsPackaged()) return true;
        // Descendants can inherit file virtualization without a package identity.
        string actual = DatabasePath(file);
        return actual.IndexOf(@"\AppData\Local\Packages\", StringComparison.OrdinalIgnoreCase) >= 0 &&
            actual.IndexOf(@"\LocalCache\", StringComparison.OrdinalIgnoreCase) >= 0;
    }

    public static void Attach() {
        if (job != IntPtr.Zero) return;
        IntPtr handle = CreateJobObject(IntPtr.Zero, null);
        if (handle == IntPtr.Zero) throw new Win32Exception();
        var limits = new ExtendedLimits();
        limits.Basic.LimitFlags = 0x2000; // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE; no breakaway
        if (!SetInformationJobObject(handle, 9, ref limits, (uint)Marshal.SizeOf(limits)) ||
            !AssignProcessToJobObject(handle, Process.GetCurrentProcess().Handle)) {
            int error = Marshal.GetLastWin32Error();
            CloseHandle(handle);
            throw new Win32Exception(error, "Cannot attach development process lifetime guard.");
        }
        job = handle; // Intentionally held until the OS closes all handles on process exit.
    }
}
