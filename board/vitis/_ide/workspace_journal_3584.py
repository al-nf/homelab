# 2025-11-21T15:54:25.109391100
import vitis

client = vitis.create_client()
client.set_workspace(path="vitis")

advanced_options = client.create_advanced_options_dict(dt_overlay="0")

platform = client.create_platform_component(name = "zynq_platform",hw_design = "$COMPONENT_LOCATION/../../vivado/zynq_bd_wrapper.xsa",os = "standalone",cpu = "ps7_cortexa9_0",domain_name = "standalone_ps7_cortexa9_0",generate_dtb = False,advanced_options = advanced_options,compiler = "gcc")

platform = client.get_component(name="zynq_platform")
status = platform.update_desc(desc="")

status = platform.update_desc(desc="")

status = platform.update_desc(desc="")

comp = client.create_app_component(name="ethernet-receiver",platform = "$COMPONENT_LOCATION/../zynq_platform/export/zynq_platform/zynq_platform.xpfm",domain = "standalone_ps7_cortexa9_0")

vitis.dispose()

