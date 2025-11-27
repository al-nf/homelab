# 2025-11-24T00:50:17.434624500
import vitis

client = vitis.create_client()
client.set_workspace(path="vitis")

platform = client.get_component(name="zynq_platform")
status = platform.build()

comp = client.get_component(name="ethernet-receiver")
comp.build()

vitis.dispose()

