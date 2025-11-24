
cmake_minimum_required(VERSION 3.15)
project(bsp)

# Use CMAKE_LIBRARY_PATH in link_directories
link_directories(${CMAKE_LIBRARY_PATH})
        
set(DRIVER_TARGETS gpio;coresightps_dcc;devcfg;dmaps;emacps;gpiops;scugic;scutimer;scuwdt;sdps;uartps;usbps;xadcps)
set(DRIVER_LOCATIONS C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/gpio_v4_12;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/coresightps_dcc_v1_12;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/devcfg_v3_8;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/dmaps_v2_11;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/emacps_v3_22;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/gpiops_v3_14;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/scugic_v5_5;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/scutimer_v2_7;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/scuwdt_v2_6;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/sdps_v4_4;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/uartps_v3_16;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/usbps_v2_9;C:/Xilinx/2025.1.1/Vitis/data/embeddedsw/XilinxProcessorIPLib/drivers/xadcps_v2_7)

list(LENGTH DRIVER_TARGETS no_of_drivers)
set(index 0)

while(${index} LESS ${no_of_drivers})
    list(GET DRIVER_TARGETS ${index} drv)
    list(GET DRIVER_LOCATIONS ${index} drv_dir)
    set(exp_dir "${drv_dir}/examples")
    add_custom_target(
        ${drv}_example ALL
        COMMAND lopper -f -O C:/Users/alanf/dev/desk-cube/board/vitis/zynq_platform/ps7_cortexa9_0/standalone_ps7_cortexa9_0/bsp/libsrc/${drv} C:/Users/alanf/dev/desk-cube/board/vitis/zynq_platform/ps7_cortexa9_0/standalone_ps7_cortexa9_0/bsp/hw_artifacts/ps7_cortexa9_0_baremetal.dts -- bmcmake_metadata_xlnx ps7_cortexa9_0 ${exp_dir} drvcmake_metadata
        BYPRODUCTS x${drv}_exlist.yaml)
    MATH(EXPR index "${index}+1")
endwhile()
