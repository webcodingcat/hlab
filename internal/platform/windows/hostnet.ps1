# The host's side of the lab (hostnet.go), run by pshost: the Hyper-V
# switches, the host NIC's addresses and routes, and the NRPT rules that send
# the host's lookups of the lab's names to the lab. They need administrator
# rights.
#
# Failures and lists follow hyperv.ps1: "<kind>: <message>", and a leading
# comma before a list.
#
# Addresses and routes go to the active store only: they are gone after
# Windows restarts, and the next start of HLab sets them again. The switches
# stay; a start takes them over.

Import-Module Hyper-V

function Get-HostNIC {
    # The host's NIC on an internal switch.
    param([string] $Switch)
    Get-NetAdapter -Name "vEthernet ($Switch)" -ErrorAction Stop
}

function Add-Switch {
    # Creates a switch of a type, or takes over the one that is there.
    param([string] $Name, [string] $Type)
    $switch = Get-VMSwitch -Name ([WildcardPattern]::Escape($Name)) -ErrorAction SilentlyContinue
    if ($null -eq $switch) {
        New-VMSwitch -Name $Name -SwitchType $Type | Out-Null
    } elseif ($switch.SwitchType.ToString() -ne $Type) {
        throw "exists: switch $Name is $($switch.SwitchType), not $Type"
    }
}

function Test-Automatic {
    # Whether Windows gave the address itself: link-local addresses (IPv6
    # fe80::, IPv4 169.254 without DHCP), which SetUp leaves alone.
    param($Address)
    $Address.PrefixOrigin.ToString() -eq 'WellKnown'
}

@{
    'used-ranges' = {
        param($Arguments)
        # every route and address outside the lab's own switches; Go finds
        # the overlaps
        $own = "vEthernet ($($Arguments.switch_prefix)*"
        $routes = Get-NetRoute -PolicyStore ActiveStore |
            Where-Object { $_.InterfaceAlias -notlike $own } |
            ForEach-Object { @{ prefix = $_.DestinationPrefix; interface = $_.InterfaceAlias } }
        $addresses = Get-NetIPAddress -PolicyStore ActiveStore |
            Where-Object { $_.InterfaceAlias -notlike $own } |
            ForEach-Object {
                # link-local IPv6 addresses carry their zone: fe80::1%12
                $address = ($_.IPAddress -split '%')[0]
                @{ prefix = "$address/$($_.PrefixLength)"; interface = $_.InterfaceAlias }
            }
        , @(@($routes) + @($addresses))
    }

    'set-up'      = {
        param($Link)
        Add-Switch $Link.switch 'Internal'
        foreach ($name in $Link.private_switches) {
            Add-Switch $name 'Private'
        }
        $index = (Get-HostNIC $Link.switch).ifIndex
        # the addresses are static: no DHCP client waits on the link
        Set-NetIPInterface -InterfaceIndex $index -Dhcp Disabled -PolicyStore ActiveStore
        $present = @(Get-NetIPAddress -InterfaceIndex $index -PolicyStore ActiveStore -ErrorAction SilentlyContinue |
                Where-Object { -not (Test-Automatic $_) })
        foreach ($address in $present) {
            if ("$($address.IPAddress)/$($address.PrefixLength)" -notin $Link.addresses) {
                Remove-NetIPAddress -InputObject $address -Confirm:$false
            }
        }
        $kept = @($present | ForEach-Object { "$($_.IPAddress)/$($_.PrefixLength)" })
        foreach ($wanted in $Link.addresses) {
            if ($wanted -notin $kept) {
                $address, $length = $wanted -split '/'
                New-NetIPAddress -InterfaceIndex $index -IPAddress $address -PrefixLength $length `
                    -PolicyStore ActiveStore | Out-Null
            }
        }
        # routes through the appliance; the on-link routes of the addresses
        # come by themselves
        $wantedRoutes = @($Link.routes | ForEach-Object { "$($_.prefix) $($_.next_hop)" })
        $presentRoutes = @(Get-NetRoute -InterfaceIndex $index -PolicyStore ActiveStore -ErrorAction SilentlyContinue |
                Where-Object { $_.NextHop -notin '0.0.0.0', '::' })
        foreach ($route in $presentRoutes) {
            if ("$($route.DestinationPrefix) $($route.NextHop)" -notin $wantedRoutes) {
                Remove-NetRoute -InputObject $route -Confirm:$false
            }
        }
        $keptRoutes = @($presentRoutes | ForEach-Object { "$($_.DestinationPrefix) $($_.NextHop)" })
        foreach ($route in $Link.routes) {
            if ("$($route.prefix) $($route.next_hop)" -notin $keptRoutes) {
                New-NetRoute -InterfaceIndex $index -DestinationPrefix $route.prefix `
                    -NextHop $route.next_hop -PolicyStore ActiveStore | Out-Null
            }
        }
    }

    'tear-down'   = {
        param($Arguments)
        # removing the internal switch removes the host's NIC with its
        # addresses and routes; VMs keep their NICs, without a switch
        foreach ($name in $Arguments.switches) {
            Get-VMSwitch -Name ([WildcardPattern]::Escape($name)) -ErrorAction SilentlyContinue |
                Remove-VMSwitch -Force
        }
    }

    'dns-apply'   = {
        param($Arguments)
        $wanted = @($Arguments.rules | ForEach-Object { "$($_.namespace)=$($_.servers -join ',')" })
        $present = @(Get-DnsClientNrptRule | Where-Object Comment -EQ $Arguments.comment)
        $changed = $false
        $kept = @()
        foreach ($rule in $present) {
            $key = "$($rule.Namespace -join ',')=$($rule.NameServers -join ',')"
            if ($key -in $wanted) {
                $kept += $key
            } else {
                Remove-DnsClientNrptRule -Name $rule.Name -Force
                $changed = $true
            }
        }
        foreach ($rule in $Arguments.rules) {
            if ("$($rule.namespace)=$($rule.servers -join ',')" -notin $kept) {
                Add-DnsClientNrptRule -Namespace $rule.namespace -NameServers $rule.servers `
                    -Comment $Arguments.comment
                $changed = $true
            }
        }
        # the cache would answer from what it learned before the rule
        if ($changed) {
            Clear-DnsClientCache
        }
    }
}
