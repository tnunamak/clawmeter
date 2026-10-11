[CmdletBinding()]
param([Parameter(Mandatory)][string]$BinaryPath)

$ErrorActionPreference = "Stop"

Add-Type -TypeDefinition @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;

public static class ClawmeterResourceReader {
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    private static extern IntPtr LoadLibraryEx(string path, IntPtr file, uint flags);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr FindResource(IntPtr module, IntPtr name, IntPtr type);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern uint SizeofResource(IntPtr module, IntPtr resource);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr LoadResource(IntPtr module, IntPtr resource);
    [DllImport("kernel32.dll", SetLastError = true)]
    private static extern IntPtr LockResource(IntPtr resource);
    [DllImport("kernel32.dll")]
    private static extern bool FreeLibrary(IntPtr module);

    public static byte[] Read(string path, int type, int id) {
        const uint LOAD_LIBRARY_AS_DATAFILE = 2;
        IntPtr module = LoadLibraryEx(path, IntPtr.Zero, LOAD_LIBRARY_AS_DATAFILE);
        if (module == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
        try {
            IntPtr resource = FindResource(module, (IntPtr)id, (IntPtr)type);
            if (resource == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
            int size = checked((int)SizeofResource(module, resource));
            IntPtr loaded = LoadResource(module, resource);
            if (loaded == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
            IntPtr data = LockResource(loaded);
            if (data == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error());
            byte[] bytes = new byte[size];
            Marshal.Copy(data, bytes, 0, size);
            return bytes;
        } finally {
            FreeLibrary(module);
        }
    }
}
'@

$binary = (Resolve-Path $BinaryPath).Path
$manifestBytes = [ClawmeterResourceReader]::Read($binary, 24, 1)
$manifestText = [System.Text.Encoding]::UTF8.GetString($manifestBytes).TrimStart([char]0xFEFF)
[xml]$manifest = $manifestText
$awareness = $manifest.SelectSingleNode("//*[local-name()='dpiAwareness']")
if (!$awareness -or $awareness.InnerText -notmatch '^\s*PerMonitorV2(\s*,|\s*$)') {
    throw "$binary has no per-monitor-v2 DPI-awareness manifest"
}

[void][ClawmeterResourceReader]::Read($binary, 16, 1)
Write-Host "PASS: $binary contains a per-monitor-v2 application manifest and version resource"
