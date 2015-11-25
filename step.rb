require 'optparse'
require 'pathname'
require 'timeout'
require_relative 'utils/logger'
require_relative 'utils/simulator'

@mdtool = "\"/Applications/Xamarin Studio.app/Contents/MacOS/mdtool\""
@mono = '/Library/Frameworks/Mono.framework/Versions/Current/bin/mono'
@nuget = '/Library/Frameworks/Mono.framework/Versions/Current/bin/nuget'

# -----------------------
# --- functions
# -----------------------

def to_bool(value)
  return true if value == true || value =~ (/^(true|t|yes|y|1)$/i)
  return false if value == false || value.nil? || value == '' || value =~ (/^(false|f|no|n|0)$/i)
  fail_with_message("Invalid value for Boolean: \"#{value}\"")
end

def run_calabash_test!(feautes)
  base_directory = File.dirname(feautes)
  Dir.chdir(base_directory) {
    puts
    puts "cucumber #{feautes}"
    system("cucumber #{feautes}")
    fail_with_message('cucumber -- failed') unless $?.success?
  }
end

# -----------------------
# --- main
# -----------------------

#
# Input validation
options = {
  project: nil,
  features: nil,
  configuration: nil,
  platform: nil,
  builder: nil,
  clean_build: true,
  device: nil,
  os: nil
}

parser = OptionParser.new do|opts|
  opts.banner = 'Usage: step.rb [options]'
  opts.on('-t', '--feautes calabash', 'Calabash features') { |t| options[:features] = t unless t.to_s == '' }
  opts.on('-d', '--device device', 'Device') { |d| options[:device] = d unless d.to_s == '' }
  opts.on('-o', '--os os', 'OS') { |o| options[:os] = o unless o.to_s == '' }
  opts.on('-h', '--help', 'Displays Help') do
    exit
  end
end
parser.parse!

fail_with_message('No features folder found') unless options[:features] && File.exist?(options[:features])
fail_with_message('simulator_device not specified') unless options[:device]
fail_with_message('simulator_os_version not specified') unless options[:os]

udid = simulator_udid(options[:device], options[:os])
fail_with_message('failed to get simulator udid') unless udid

ENV['DEVICE_TARGET'] = "#{udid}"

#
# Print configs
puts
puts '========== Configs =========='
puts " * features: #{options[:features]}"
puts " * simulator_device: #{options[:device]}"
puts " * simulator_os: #{options[:os]}"
puts " * simulator_UDID: #{udid}"

#
# Run calabash test
puts
puts '=> run calabash test'
run_calabash_test!(options[:features])

puts
puts '(i) The result is: succeeded'
system('envman add --key BITRISE_XAMARIN_TEST_RESULT --value succeeded')