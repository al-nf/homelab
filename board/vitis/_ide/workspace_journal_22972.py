# 2025-11-24T00:12:41.332167600
import vitis

client = vitis.create_client()
client.set_workspace(path="vitis")

platform = client.get_component(name="zynq_platform")
status = platform.build()

comp = client.get_component(name="ethernet-receiver")
comp.build()

status = platform.build()

comp.build()

status = platform.build()

comp.build()

status = platform.build()

vitis.dispose()

