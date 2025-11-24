# 2025-11-23T23:47:25.042353400
import vitis

client = vitis.create_client()
client.set_workspace(path="vitis")

platform = client.get_component(name="zynq_platform")
status = platform.build()

comp = client.get_component(name="ethernet-receiver")
comp.build()

vitis.dispose()

