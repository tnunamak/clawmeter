[CmdletBinding(DefaultParameterSetName = "Binary")]
param(
    [Parameter(Mandatory, ParameterSetName = "Binary")][string]$BinaryPath,
    [Parameter(Mandatory, ParameterSetName = "SelfTest")][switch]$SelfTest
)

$ErrorActionPreference = "Stop"

function Assert-DpiManifest {
    param([xml]$Manifest, [string]$Source)

    $namespaces = [System.Xml.XmlNamespaceManager]::new($Manifest.NameTable)
    $namespaces.AddNamespace("asmv1", "urn:schemas-microsoft-com:asm.v1")
    $namespaces.AddNamespace("asmv3", "urn:schemas-microsoft-com:asm.v3")
    $namespaces.AddNamespace("ws2016", "http://schemas.microsoft.com/SMI/2016/WindowsSettings")
    $awareness = $Manifest.SelectNodes(
        "/asmv1:assembly/asmv3:application/asmv3:windowsSettings/ws2016:dpiAwareness",
        $namespaces
    )
    # Accept the intended setting with its optional supported fallback, not
    # an arbitrary prefix, a duplicate, or a lookalike elsewhere in the XML.
    if ($awareness.Count -ne 1 -or
        $awareness[0].InnerText -cnotmatch '^\s*PerMonitorV2\s*(,\s*PerMonitor\s*)?$') {
        throw "$Source has no valid per-monitor-v2 DPI-awareness application setting"
    }
}

# Cross-platform fixture checks do not load any Windows libraries.
# Run with: pwsh ./packaging/windows/verify-dpi-manifest.ps1 -SelfTest
if ($SelfTest) {
    [xml]$valid = Get-Content -Raw (Join-Path $PSScriptRoot "clawmeter.manifest")
    Assert-DpiManifest -Manifest $valid -Source "committed manifest"
    Write-Host "PASS: committed DPI manifest"

    [xml]$wrongNamespace = $valid.OuterXml.Replace(
        "http://schemas.microsoft.com/SMI/2016/WindowsSettings", "urn:invalid")
    [xml]$wrongLocation = $valid.OuterXml
    $node = $wrongLocation.GetElementsByTagName(
        "dpiAwareness", "http://schemas.microsoft.com/SMI/2016/WindowsSettings")[0]
    [void]$node.ParentNode.RemoveChild($node)
    [void]$wrongLocation.DocumentElement.AppendChild($node)
    [xml]$wrongValue = $valid.OuterXml.Replace("PerMonitorV2, PerMonitor", "PerMonitorV2, Invalid")

    foreach ($fixture in @(
        @{ Name = "wrong namespace"; Manifest = $wrongNamespace },
        @{ Name = "wrong location"; Manifest = $wrongLocation },
        @{ Name = "unsupported value"; Manifest = $wrongValue }
    )) {
        $rejected = $false
        try {
            Assert-DpiManifest -Manifest $fixture.Manifest -Source $fixture.Name
        } catch {
            $rejected = $true
        }
        if (!$rejected) {
            throw "FAIL: verifier accepted $($fixture.Name)"
        }
        Write-Host "PASS: rejected $($fixture.Name)"
    }
    return
}

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
Assert-DpiManifest -Manifest $manifest -Source $binary

[void][ClawmeterResourceReader]::Read($binary, 16, 1)
Write-Host "PASS: $binary contains a per-monitor-v2 application manifest and version resource"
